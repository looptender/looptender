import XCTest
@testable import KeepgoingMenuCore

final class ManagementModeTests: XCTestCase {
    func testActualWireModesLegacyAndConfirmedManagementEntries() throws {
        try ManagementModeFixture.run()
    }
}
