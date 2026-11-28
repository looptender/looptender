import Foundation

/// Compile with core sources and ManagementModeFixture.swift; no app/CLI is run.
@main struct ManagementModeSmoke {
    static func main() {
        do {
            try ManagementModeFixture.run()
            print("Native management PASS: exact all/controls/read_only/legacy modes, confirmation/default cancel, scoped account opener, intent boundary and tracking guard; no runtime calls")
        } catch {
            fputs("Native management fixture failed: \(error)\n", stderr)
            exit(1)
        }
    }
}
