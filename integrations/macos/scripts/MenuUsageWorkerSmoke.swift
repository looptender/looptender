import Foundation
import Darwin

private final class FakeWorker: UsageWorkerChild {
    var isRunning = true
    var stops = 0
    func stop() { stops += 1; isRunning = false }
}
@main struct MenuUsageWorkerSmoke {
    static func wait(_ path: String) {
        let deadline = Date().addingTimeInterval(3)
        while Date() < deadline { if FileManager.default.fileExists(atPath: path) { return }; Thread.sleep(forTimeInterval: 0.01) }
        fatalError("Owned child did not reach \(URL(fileURLWithPath: path).lastPathComponent)")
    }
    static func main() throws {
        let args = CommandLine.arguments
        if args.count == 4, args[1] == "--abrupt-parent" {
            setenv("HOME", args[3], 1)
            let child = try LocalClient(executable: URL(fileURLWithPath: args[2])).startUsageWorker()
            wait(args[3] + "/started")
            guard child.isRunning else { fatalError("child exited before parent") }
            exit(0) // bypass stop/deinit/applicationWillTerminate, simulating app death
        }
        guard args.count == 3 else { fatalError("Expected fictional child executable and private fixture directory") }
        let now = Date(), first = FakeWorker(), second = FakeWorker()
        var starts = 0
        let controller = MenuUsageWorker { starts += 1; return starts == 1 ? first : second }
        controller.update(eligible: false, suspended: false, now: now)
        controller.update(eligible: true, suspended: true, now: now)
        precondition(starts == 0)
        controller.update(eligible: true, suspended: false, now: now)
        controller.update(eligible: true, suspended: false, now: now.addingTimeInterval(60))
        precondition(starts == 1)
        first.isRunning = false
        controller.update(eligible: true, suspended: false, now: now.addingTimeInterval(120))
        precondition(starts == 1)
        controller.update(eligible: true, suspended: false, now: now.addingTimeInterval(300))
        precondition(starts == 2)
        controller.update(eligible: false, suspended: false, now: now.addingTimeInterval(360))
        precondition(second.stops == 1)

        let root = URL(fileURLWithPath: args[2]), graceful = root.appendingPathComponent("graceful"), abrupt = root.appendingPathComponent("abrupt")
        try FileManager.default.createDirectory(at: graceful, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: abrupt, withIntermediateDirectories: true)
        setenv("HOME", graceful.path, 1); setenv("BB_THREAD_ID", "thr_example_worker", 1)
        let child = try LocalClient(executable: URL(fileURLWithPath: args[1])).startUsageWorker()
        wait(graceful.appendingPathComponent("started").path)
        precondition(child.isRunning)
        child.stop()
        wait(graceful.appendingPathComponent("stopped").path)
        let parent = Process()
        parent.executableURL = URL(fileURLWithPath: args[0])
        parent.arguments = ["--abrupt-parent", args[1], abrupt.path]
        try parent.run(); parent.waitUntilExit()
        precondition(parent.terminationStatus == 0)
        wait(abrupt.appendingPathComponent("stopped").path)
        print("Menu-owned worker PASS: opt-in only, one child, bounded restart, revoke/suspend, exact arguments, scrubbed identity, graceful EOF and abrupt parent exit; no panel or provider")
    }
}
