import XCTest
@testable import KeepgoingMenuCore

final class MenuBuildTests: XCTestCase {
    func testCapturedMetadataIsNotReplacedByAnActionReceiptOrUnavailableCLI() {
        var info: [String: Any] = ["CFBundleShortVersionString": "0.1.0", "LooptenderSourceRevision": String(repeating: "a", count: 40)]
        let stamp = MenuBuildStamp.capture(info)
        info["LooptenderSourceRevision"] = String(repeating: "b", count: 40)
        let model = MenuBuilder.build(.unavailable, now: Date(), notice: "Example action completed", buildStamp: stamp)
        XCTAssertEqual(model.rows[1].title, "Menu 0.1.0 · build aaaaaaaa")
        XCTAssertEqual(model.rows[2].title, "CLI build unavailable")
        XCTAssertEqual(model.rows.last?.title, "Example action completed")
        XCTAssertEqual(MenuBuildStamp.capture([:]).text, "Menu unknown · build unknown")
        XCTAssertEqual(MenuBuildStamp(version: "0.1.0", revision: "not a revision").text, "Menu 0.1.0 · build unknown")
    }

    func testInstalledMismatchDefersUnderTrackedMenu() {
        let stamp = MenuBuildStamp(version: "0.1.0", revision: String(repeating: "a", count: 40))
        let now = Date()
        let before = MenuBuilder.build(.notRead, now: now, buildStamp: stamp)
        let after = MenuBuilder.build(.notRead, now: now, update: UpdateOffer(availableLabel: "bbbbbbbb", automatic: false), buildStamp: stamp)
        XCTAssertTrue(after.rows.contains { $0.title == "Installed menu differs · bbbbbbbb" })
        XCTAssertEqual(MenuBuilder.updateMode(shown: before.rows, next: after.rows, itemCount: before.rows.count, menuOpen: true, hasHighlight: true), .deferred)
        XCTAssertEqual(before.rows.first?.command, after.rows.first?.command)
    }
}
