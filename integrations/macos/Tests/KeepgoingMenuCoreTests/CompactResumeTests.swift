import XCTest
@testable import KeepgoingMenuCore

final class CompactResumeTests: XCTestCase {
    func testCompactResumePreservesStateIntentAndGuards() throws {
        try CompactResumeFixture.run()
    }
}
