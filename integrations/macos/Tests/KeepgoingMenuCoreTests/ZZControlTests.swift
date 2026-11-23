import XCTest

/// Throwaway CI control: a Swift test that fails.
final class ZZControlTests: XCTestCase {
    func testDeliberateFailure() {
        XCTFail("deliberate Swift test failure")
    }
}
