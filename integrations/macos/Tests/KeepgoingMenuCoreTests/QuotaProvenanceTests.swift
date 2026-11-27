import XCTest
@testable import KeepgoingMenuCore

final class QuotaProvenanceTests: XCTestCase {
    func testPinnedProducerLegacyAndMalformedProvenance() throws {
        try QuotaProvenanceFixture.run()
    }
}
