import Foundation
import Darwin

/// An owned child, never a service. Closing its private stdin lease cancels
/// provider work even when the menu exits without applicationWillTerminate.
public protocol UsageWorkerChild: AnyObject {
    var isRunning: Bool { get }
    func stop()
}

public final class MenuUsageWorker {
    private let launch: () throws -> UsageWorkerChild
    private var child: UsageWorkerChild?
    private var retryAt = Date.distantPast
    public init(launch: @escaping () throws -> UsageWorkerChild) { self.launch = launch }
    /// Called on the menu's main queue after a successful saved list read. A
    /// refused/crashed child is restarted at most once per five minutes; provider
    /// cadence and backoff are separately persisted by the CLI.
    public func update(eligible: Bool, suspended: Bool, now: Date) {
        guard eligible && !suspended else { stop(); return }
        if child?.isRunning == true || now < retryAt { return }
        child?.stop(); child = nil
        retryAt = now.addingTimeInterval(300)
        child = try? launch()
    }
    public func stop() { child?.stop(); child = nil }
    deinit { stop() }
}

private final class OwnedUsageWorker: UsageWorkerChild {
    private let process: Process
    private var lease: FileHandle?
    init(process: Process, lease: FileHandle) { self.process = process; self.lease = lease }
    var isRunning: Bool { process.isRunning }
    func stop() {
        guard let lease else { return }
        self.lease = nil
        try? lease.close()
        // Escalation targets only this owned Process, never discovery or a
        // runtime pid file. Normal EOF cancellation exits before either timer.
        let owned = process
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 3) { if owned.isRunning { owned.terminate() } }
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 5) { if owned.isRunning { Darwin.kill(owned.processIdentifier, SIGKILL) } }
    }
    deinit { stop() }
}

extension LocalClient {
    /// One fixed child command, separate from the short-lived read/apply calls.
    /// The CLI revalidates explicit grants before starting and on every tick.
    public func startUsageWorker() throws -> UsageWorkerChild {
        guard executable.isFileURL, executable.path.hasPrefix("/"), FileManager.default.isExecutableFile(atPath: executable.path) else { throw ClientFailure.unavailable }
        let process = Process(), input = Pipe()
        process.executableURL = executable
        process.arguments = ["accounts", "usage-worker", "--owner-stdin"]
        process.environment = LocalClient.environment(ProcessInfo.processInfo.environment)
        process.standardInput = input
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        do { try process.run() } catch {
            try? input.fileHandleForReading.close(); try? input.fileHandleForWriting.close()
            throw ClientFailure.unavailable
        }
        try? input.fileHandleForReading.close()
        let child = OwnedUsageWorker(process: process, lease: input.fileHandleForWriting)
        do { try input.fileHandleForWriting.write(contentsOf: Data("looptender-menu-usage-v1\n".utf8)) }
        catch { child.stop(); throw ClientFailure.unavailable }
        return child
    }
}
