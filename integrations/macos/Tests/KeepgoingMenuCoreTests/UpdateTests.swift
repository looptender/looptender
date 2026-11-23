import XCTest
@testable import KeepgoingMenuCore

final class MemoryStore: UpdateStore {
    var values: [String: String] = [:]
    func string(forKey key: String) -> String? { values[key] }
    func set(_ value: String?, forKey key: String) { values[key] = value }
}

final class UpdateTests: XCTestCase {
    let running = BuildIdentity(path: "/i/builds/aaa/KeepgoingMenu", stamp: "1-1", label: "aaa")
    let next = BuildIdentity(path: "/i/builds/bbb/KeepgoingMenu", stamp: "1-2", label: "bbb")
    let third = BuildIdentity(path: "/i/builds/ccc/KeepgoingMenu", stamp: "1-3", label: "ccc")

    func updater(_ store: MemoryStore = MemoryStore()) -> MenuUpdater { MenuUpdater(running: running, store: store) }

    // MARK: the comparison

    func testSameBuildOrNoInstallSaysNothing() {
        let u = updater()
        XCTAssertEqual(u.step(installed: nil, menuOpen: false, controlInFlight: false), .nothing)
        XCTAssertEqual(u.step(installed: running, menuOpen: false, controlInFlight: false), .nothing)
    }

    func testTheSamePathWithAReplacedFileIsADifferentBuild() {
        let replaced = BuildIdentity(path: running.path, stamp: "9-9", label: running.label)
        XCTAssertNotEqual(replaced.id, running.id)
        XCTAssertEqual(updater().step(installed: replaced, menuOpen: false, controlInFlight: false), .relaunch(label: "aaa"))
    }

    // MARK: automatic, held back, and one attempt for each build

    func testAutomaticRelaunchHappensOnlyWhenClosedAndIdle() {
        let u = updater()
        XCTAssertTrue(u.automatic, "on by default")
        // Open, or a change in flight: held back, and nothing is said while a relaunch is coming.
        XCTAssertEqual(u.step(installed: next, menuOpen: true, controlInFlight: false), .offer(label: "bbb", notify: false))
        XCTAssertEqual(u.step(installed: next, menuOpen: false, controlInFlight: true), .offer(label: "bbb", notify: false))
        XCTAssertEqual(u.step(installed: next, menuOpen: false, controlInFlight: false), .relaunch(label: "bbb"))
        // Being held back used nothing up: the attempt is only recorded when one begins.
        XCTAssertTrue(u.beginAttempt(next))
    }

    func testAPostponedAttemptIsGivenBackAndCostsNothing() {
        let store = MemoryStore()
        let u = updater(store)
        XCTAssertTrue(u.beginAttempt(next))
        XCTAssertFalse(u.beginAttempt(next), "one attempt for each build")
        u.postponeAttempt(next)
        XCTAssertEqual(u.step(installed: next, menuOpen: false, controlInFlight: false), .relaunch(label: "bbb"))
        XCTAssertTrue(u.beginAttempt(next))
        // A postponed attempt leaves no "updated" claim behind.
        u.postponeAttempt(next)
        XCTAssertNil(MenuUpdater(running: next, store: store).pendingUpdate())
    }

    func testAPathRewrittenInPlaceCannotGiveUnlimitedAttempts() {
        let store = MemoryStore()
        let u = updater(store)
        let start = Date(timeIntervalSince1970: 1_900_000_000)
        for i in 0..<MenuUpdater.maxAttemptsPerHour {
            let rewritten = BuildIdentity(path: next.path, stamp: "1-\(10 + i)", label: next.label)
            XCTAssertTrue(u.beginAttempt(rewritten, now: start.addingTimeInterval(Double(i) * 60)))
        }
        let again = BuildIdentity(path: next.path, stamp: "1-99", label: next.label)
        XCTAssertEqual(u.step(installed: again, menuOpen: false, controlInFlight: false, now: start.addingTimeInterval(600)), .offer(label: "bbb", notify: true))
        XCTAssertFalse(u.beginAttempt(again, now: start.addingTimeInterval(600)))
        // An hour after the first attempt, one is allowed again.
        XCTAssertEqual(u.step(installed: again, menuOpen: false, controlInFlight: false, now: start.addingTimeInterval(3601)), .relaunch(label: "bbb"))
    }

    func testARelaunchIsAttemptedOncePerInstalledBuildSoItNeverLoops() {
        let store = MemoryStore()
        let u = updater(store)
        XCTAssertEqual(u.step(installed: next, menuOpen: false, controlInFlight: false), .relaunch(label: "bbb"))
        XCTAssertTrue(u.beginAttempt(next))
        u.attemptFailed(RelaunchFailure("the new menu program is missing or not runnable"))
        XCTAssertEqual(u.lastFailure, "the new menu program is missing or not runnable")
        // The same build again, even from a later menu: no second attempt, an offer instead, said once.
        let again = updater(store)
        XCTAssertEqual(again.step(installed: next, menuOpen: false, controlInFlight: false), .offer(label: "bbb", notify: true))
        XCTAssertEqual(again.step(installed: next, menuOpen: false, controlInFlight: false), .offer(label: "bbb", notify: false))
        // A newer build is a new attempt.
        XCTAssertEqual(again.step(installed: third, menuOpen: false, controlInFlight: false), .relaunch(label: "ccc"))
    }

    // MARK: saying it, once for each build

    func testWithAutomaticOffTheOfferIsSaidOncePerBuildAndNeverRelaunches() {
        let store = MemoryStore()
        let u = updater(store)
        u.automatic = false
        XCTAssertEqual(store.values["updateAutomatically"], "off")
        XCTAssertEqual(u.step(installed: next, menuOpen: false, controlInFlight: false), .offer(label: "bbb", notify: true))
        for _ in 0..<5 { XCTAssertEqual(u.step(installed: next, menuOpen: false, controlInFlight: false), .offer(label: "bbb", notify: false)) }
        // Quitting and starting the menu again does not say it again.
        XCTAssertEqual(updater(store).step(installed: next, menuOpen: true, controlInFlight: false), .offer(label: "bbb", notify: false))
        // A newer build is said once too.
        XCTAssertEqual(u.step(installed: third, menuOpen: false, controlInFlight: false), .offer(label: "ccc", notify: true))
        XCTAssertEqual(u.step(installed: third, menuOpen: false, controlInFlight: false), .offer(label: "ccc", notify: false))
    }

    func testAnAutomaticRelaunchSaysUpdatedOnceInTheNewMenuInsteadOfAvailable() {
        let store = MemoryStore()
        let old = updater(store)
        XCTAssertEqual(old.step(installed: next, menuOpen: false, controlInFlight: false), .relaunch(label: "bbb"))
        XCTAssertTrue(old.beginAttempt(next))
        XCTAssertNil(store.values["updateNotifiedBuild"], "no 'available' was said")
        // The new menu is the build that was installed.
        let fresh = MenuUpdater(running: next, store: store)
        XCTAssertEqual(fresh.pendingUpdate()?.text, "looptender menu updated to bbb")
        XCTAssertEqual(fresh.pendingUpdate()?.text, "looptender menu updated to bbb", "not used up until it is finished")
        fresh.finishUpdate()
        XCTAssertNil(fresh.pendingUpdate(), "once")
        XCTAssertNil(MenuUpdater(running: next, store: store).pendingUpdate(), "and not after another start")
    }

    func testAFailedAutomaticAttemptOrARequestedOneNeverClaimsToHaveUpdated() {
        let store = MemoryStore()
        let u = updater(store)
        XCTAssertTrue(u.beginAttempt(next))
        u.attemptFailed(RelaunchFailure("x"))
        XCTAssertNil(MenuUpdater(running: next, store: store).pendingUpdate())
        let manual = MemoryStore()
        let m = MenuUpdater(running: running, store: manual)
        m.relaunchByRequest(installed: next)
        XCTAssertNil(MenuUpdater(running: next, store: manual).pendingUpdate())
    }

    func testNoticeWords() {
        XCTAssertEqual(MenuUpdater.availableNotice("0b4e12399667"), "looptender menu: update available (0b4e12399667)")
    }

    // MARK: finding the installed build

    func withInstall(_ body: (URL) throws -> Void) throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("menu-update-" + UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        try body(root.resolvingSymlinksInPath())
    }

    func makeBuild(_ root: URL, _ id: String, contents: String = "x") throws {
        let dir = root.appendingPathComponent("builds/" + id)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let file = dir.appendingPathComponent("KeepgoingMenu")
        try contents.write(to: file, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: file.path)
    }

    func testMovingTheCurrentLinkIsFoundFromTheLaunchPathAndFromTheBuildsLayout() throws {
        try withInstall { root in
            try makeBuild(root, "aaa111")
            try makeBuild(root, "bbb222", contents: "yy")
            let link = root.appendingPathComponent("current")
            try FileManager.default.createSymbolicLink(atPath: link.path, withDestinationPath: "builds/aaa111")
            let launch = link.appendingPathComponent("KeepgoingMenu").path
            let me = try XCTUnwrap(InstalledBuild.identity(of: launch))
            XCTAssertEqual(me.label, "aaa111")
            XCTAssertNil(InstalledBuild.current(launchPath: launch, running: me), "nothing moved")
            try FileManager.default.removeItem(at: link)
            try FileManager.default.createSymbolicLink(atPath: link.path, withDestinationPath: "builds/bbb222")
            XCTAssertEqual(InstalledBuild.current(launchPath: launch, running: me)?.label, "bbb222")
            // Launched by the real path, the layout still shows the move.
            let real = try XCTUnwrap(InstalledBuild.identity(of: root.appendingPathComponent("builds/aaa111/KeepgoingMenu").path))
            XCTAssertEqual(InstalledBuild.current(launchPath: real.path, running: real)?.label, "bbb222")
        }
    }

    func testAFileReplacedInPlaceIsADifferentBuildAndAMissingOneIsNothing() throws {
        try withInstall { root in
            try makeBuild(root, "aaa111")
            let path = root.appendingPathComponent("builds/aaa111/KeepgoingMenu").path
            let me = try XCTUnwrap(InstalledBuild.identity(of: path))
            try "a much longer program".write(toFile: path, atomically: true, encoding: .utf8)
            XCTAssertNotNil(InstalledBuild.current(launchPath: path, running: me))
            try FileManager.default.removeItem(atPath: path)
            XCTAssertNil(InstalledBuild.current(launchPath: path, running: me), "a missing file is not an update")
            XCTAssertNil(InstalledBuild.identity(of: root.path), "a directory is not a build")
        }
    }

    func testAnInstallWithNoMenuAppIsToldApartFromNothingChanged() throws {
        try withInstall { root in
            try makeBuild(root, "aaa111")
            try FileManager.default.createDirectory(at: root.appendingPathComponent("builds/bbb222"), withIntermediateDirectories: true)
            let link = root.appendingPathComponent("current")
            try FileManager.default.createSymbolicLink(atPath: link.path, withDestinationPath: "builds/aaa111")
            let launch = link.appendingPathComponent("KeepgoingMenu").path
            let me = try XCTUnwrap(InstalledBuild.identity(of: launch))
            XCTAssertEqual(InstalledBuild.state(launchPath: launch, running: me), .same)
            try FileManager.default.removeItem(at: link)
            try FileManager.default.createSymbolicLink(atPath: link.path, withDestinationPath: "builds/bbb222")
            XCTAssertEqual(InstalledBuild.state(launchPath: launch, running: me), .noMenu(label: "bbb222"))
            try makeBuild(root, "bbb222")
            if case .newer(let found) = InstalledBuild.state(launchPath: launch, running: me) { XCTAssertEqual(found.label, "bbb222") } else { XCTFail("should be newer") }
        }
    }

    // MARK: quitting only when idle

    func testBusyWorkIsHeldFromItsStartToItsLastStepWindowsIncluded() {
        let busy = BusyCounter()
        XCTAssertFalse(busy.isBusy(menuOpen: false))
        XCTAssertTrue(busy.isBusy(menuOpen: true), "an open menu is busy")
        // A review: read, then the confirmation window, then the apply, one token throughout.
        let review = busy.begin()
        XCTAssertTrue(busy.isBusy(menuOpen: false), "while the window is up")
        // openWithControls hands over to run(): the second token is taken before the first is let go.
        let run = busy.begin()
        busy.end(review)
        XCTAssertTrue(busy.isBusy(menuOpen: false), "between the stop and the start of the panel")
        busy.end(run)
        XCTAssertFalse(busy.isBusy(menuOpen: false))
        // Letting go twice, or a token never issued, changes nothing.
        let other = busy.begin()
        busy.end(run)
        busy.end(999)
        XCTAssertEqual(busy.count, 1)
        busy.end(other)
        XCTAssertEqual(busy.count, 0)
    }

    func testTheGateAsksAgainBeforeStartingAndBeforeQuitting() {
        let checked = BuildIdentity(path: "/i/builds/bbb/KeepgoingMenu", stamp: "1-2", label: "bbb")
        XCTAssertEqual(RelaunchGate.beforeStart(busy: false, checked: checked, now: checked), .go)
        XCTAssertEqual(RelaunchGate.beforeStart(busy: true, checked: checked, now: checked), .busy)
        XCTAssertEqual(RelaunchGate.beforeStart(busy: false, checked: checked, now: BuildIdentity(path: checked.path, stamp: "9-9", label: "bbb")), .changed, "swapped after the checks")
        XCTAssertEqual(RelaunchGate.beforeStart(busy: false, checked: checked, now: BuildIdentity(path: "/i/builds/ccc/KeepgoingMenu", stamp: "1-2", label: "ccc")), .changed, "the link moved on")
        XCTAssertEqual(RelaunchGate.beforeStart(busy: false, checked: checked, now: nil), .changed)
        XCTAssertEqual(RelaunchGate.beforeQuit(busy: false, newMenuRunning: true, exitStatus: 0), .quit)
        // The person opened the menu or started a review in the moment since: keep this menu.
        XCTAssertEqual(RelaunchGate.beforeQuit(busy: true, newMenuRunning: true, exitStatus: 0), .abort)
        XCTAssertEqual(RelaunchGate.beforeQuit(busy: false, newMenuRunning: false, exitStatus: 3), .failed("the new menu exited at once (status 3)"))
        XCTAssertEqual(RelaunchGate.beforeQuit(busy: true, newMenuRunning: false, exitStatus: 3), .failed("the new menu exited at once (status 3)"))
        XCTAssertTrue(RelaunchGate.busyMessage.hasPrefix("Finish or cancel the open action first"))
    }

    func testTheToldGapsAreHandedToTheNextMenuOnce() {
        let store = MemoryStore()
        let u = updater(store)
        XCTAssertNil(u.takeTold())
        u.handOverTold("l000000000001=1893488400")
        XCTAssertEqual(MenuUpdater(running: next, store: store).takeTold(), "l000000000001=1893488400")
        XCTAssertNil(u.takeTold(), "once")
    }

    // MARK: checks before quitting

    func testRelaunchChecksRefuseABrokenBundleAndLeaveThisMenuRunning() throws {
        try withInstall { root in
            func bundle(_ name: String, plist: String?, runnable: Bool = true) throws -> BuildIdentity {
                let app = root.appendingPathComponent(name + ".app/Contents")
                try FileManager.default.createDirectory(at: app.appendingPathComponent("MacOS"), withIntermediateDirectories: true)
                let exe = app.appendingPathComponent("MacOS/KeepgoingMenu")
                try "x".write(to: exe, atomically: true, encoding: .utf8)
                try FileManager.default.setAttributes([.posixPermissions: runnable ? 0o755 : 0o644], ofItemAtPath: exe.path)
                if let plist { try plist.write(to: app.appendingPathComponent("Info.plist"), atomically: true, encoding: .utf8) }
                return BuildIdentity(path: exe.path, stamp: "1-1", label: name)
            }
            func info(_ id: String, exec: String = "KeepgoingMenu") -> String {
                "<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>CFBundleIdentifier</key><string>\(id)</string><key>CFBundleExecutable</key><string>\(exec)</string></dict></plist>"
            }
            let mine = try bundle("mine", plist: info("local.keepgoing.menu-preview"))
            let good = try bundle("good", plist: info("local.keepgoing.menu-preview"))
            XCTAssertNil(RelaunchCheck.verify(good, runningPath: mine.path, verifySignature: { _ in nil }))
            XCTAssertEqual(RelaunchCheck.verify(good, runningPath: mine.path, verifySignature: { _ in "a sealed resource is missing" })?.reason,
                           "the new app's signature does not verify: a sealed resource is missing")
            XCTAssertNotNil(RelaunchCheck.verify(try bundle("noplist", plist: nil), runningPath: mine.path, verifySignature: { _ in nil }))
            XCTAssertNotNil(RelaunchCheck.verify(try bundle("other", plist: info("someone.else")), runningPath: mine.path, verifySignature: { _ in nil }))
            XCTAssertNotNil(RelaunchCheck.verify(try bundle("wrongexec", plist: info("local.keepgoing.menu-preview", exec: "Other")), runningPath: mine.path, verifySignature: { _ in nil }))
            XCTAssertNotNil(RelaunchCheck.verify(try bundle("noexec", plist: info("local.keepgoing.menu-preview"), runnable: false), runningPath: mine.path, verifySignature: { _ in nil }))
            // A bare executable (no bundle) has no signature to check.
            let bare = root.appendingPathComponent("bare")
            try "x".write(to: bare, atomically: true, encoding: .utf8)
            try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: bare.path)
            XCTAssertNil(RelaunchCheck.verify(BuildIdentity(path: bare.path, stamp: "1-1", label: "bare"), runningPath: bare.path, verifySignature: { _ in "never asked" }))
        }
    }

    // MARK: the menu's rows

    func testTheMenuOffersTheUpdateAndTheSettingAndNothingElse() {
        let none = MenuBuilder.build(.unavailable, now: Date(), update: nil)
        XCTAssertFalse(none.rows.contains { $0.command == .relaunchMenu || $0.command == .toggleAutoUpdate }, "a menu that cannot tell its build offers neither")
        let same = MenuBuilder.build(.unavailable, now: Date(), update: UpdateOffer(availableLabel: nil, automatic: true))
        XCTAssertFalse(same.rows.contains { $0.command == .relaunchMenu })
        XCTAssertEqual(same.rows.first { $0.command == .toggleAutoUpdate }?.detail, "on")
        let offer = MenuBuilder.build(.unavailable, now: Date(), update: UpdateOffer(availableLabel: "0b4e12399667", automatic: false, failure: "the new app has no readable Info.plist"))
        XCTAssertEqual(offer.rows.first { $0.command == .relaunchMenu }?.title, "Update available: relaunch to use 0b4e12399667")
        XCTAssertEqual(offer.rows.first { $0.command == .toggleAutoUpdate }?.detail, "off")
        let none2 = MenuBuilder.build(.unavailable, now: Date(), update: UpdateOffer(availableLabel: nil, automatic: true, noMenuLabel: "bbb222"))
        XCTAssertTrue(none2.rows.contains { $0.title == "The installed build (bbb222) has no menu app, so this menu cannot update to it." })
        XCTAssertFalse(none2.rows.contains { $0.command == .relaunchMenu })
        XCTAssertTrue(offer.rows.contains { $0.title == "The last automatic relaunch did not happen: the new app has no readable Info.plist." })
        // Neither runs a CLI command.
        XCTAssertNil(MenuBuilder.arguments(.relaunchMenu))
        XCTAssertNil(MenuBuilder.arguments(.toggleAutoUpdate))
    }
}
