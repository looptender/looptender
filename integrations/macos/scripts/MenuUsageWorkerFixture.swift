import Foundation

/// Fictional executable for ownership/EOF tests. It never reads accounts or calls a provider.
@main struct MenuUsageWorkerFixture {
    static func main() throws {
        if CommandLine.arguments.dropFirst() == ["--warmup"] { return }
        guard Array(CommandLine.arguments.dropFirst()) == ["accounts", "usage-worker", "--owner-stdin"], ProcessInfo.processInfo.environment["BB_THREAD_ID"] == nil,
              let home = ProcessInfo.processInfo.environment["HOME"] else { exit(2) }
        // Bound even a failing EOF test; never leave a test child running.
        DispatchQueue.global().asyncAfter(deadline: .now() + 4) { exit(3) }
        let input = FileHandle.standardInput, hello = Data("looptender-menu-usage-v1\n".utf8)
        var received = Data()
        while received.count < hello.count {
            guard let chunk = try input.read(upToCount: hello.count - received.count), !chunk.isEmpty else { exit(2) }
            received.append(chunk)
        }
        guard received == hello else { exit(2) }
        try Data("started".utf8).write(to: URL(fileURLWithPath: home).appendingPathComponent("started"))
        _ = try input.readToEnd()
        try Data("stopped".utf8).write(to: URL(fileURLWithPath: home).appendingPathComponent("stopped"))
    }
}
