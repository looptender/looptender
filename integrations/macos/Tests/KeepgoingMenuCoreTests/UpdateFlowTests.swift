import XCTest
@testable import KeepgoingMenuCore

final class FakeChild: RunningMenu {
    var isRunning = true
    var exitStatus: Int32 = 0
    var terminated = 0, killed = 0
    /// How the child answers a polite stop: exits, or ignores it.
    var exitsOnTerminate = true
    var exitsOnKill = true
    func terminate() { terminated += 1; if exitsOnTerminate { isRunning = false } }
    func kill() { killed += 1; if exitsOnKill { isRunning = false } }
}

/// A menu app made of lists: time moves only when the test runs the clock.
final class FakeEnvironment: UpdateEnvironment {
    var menuOpen = false
    var state: InstalledBuild.State = .same
    var verifyResult: RelaunchFailure?
    var verifyFinishesLater = false
    var pendingVerify: [() -> Void] = []
    var startError: Error?
    var children: [FakeChild] = []
    var alive: Set<Int32> = []
    var processID: Int32 = 100
    var quit_ = 0
    var available: [String] = [], updated: [String] = [], told: [(String, String)] = [], notices: [String] = []
    var redraws = 0
    var queue: [(id: Int, at: TimeInterval, work: () -> Void)] = []
    var nextID = 0
    var now: TimeInterval = 0

    func installedState() -> InstalledBuild.State { state }
    func verify(_ target: BuildIdentity, runningPath: String, completion: @escaping (RelaunchFailure?) -> Void) {
        let finish = { completion(self.verifyResult) }
        if verifyFinishesLater { pendingVerify.append(finish) } else { finish() }
    }
    func start(_ target: BuildIdentity) throws -> RunningMenu {
        if let startError { throw startError }
        let child = FakeChild()
        children.append(child)
        return child
    }
    func after(_ seconds: TimeInterval, _ work: @escaping () -> Void) { nextID += 1; queue.append((nextID, now + seconds, work)) }
    func quit() { quit_ += 1 }
    func processAlive(_ pid: Int32) -> Bool { alive.contains(pid) }
    func postAvailable(_ text: String) { available.append(text) }
    func postUpdated(_ text: String) { updated.append(text) }
    func tell(_ title: String, _ text: String) { told.append((title, text)) }
    func setNotice(_ text: String) { notices.append(text) }
    func exportTold() -> String { "l000000000001=1" }
    func changed() { redraws += 1 }

    /// Runs everything due within `seconds`, earliest first (ties in the order they were set).
    func advance(_ seconds: TimeInterval) {
        let end = now + seconds
        while let index = queue.indices.filter({ queue[$0].at <= end }).min(by: { (queue[$0].at, queue[$0].id) < (queue[$1].at, queue[$1].id) }) {
            let item = queue.remove(at: index)
            now = max(now, item.at)
            item.work()
        }
        now = end
    }
}

final class UpdateFlowTests: XCTestCase {
    let running = BuildIdentity(path: "/i/builds/aaa/KeepgoingMenu", stamp: "1-1", label: "aaa")
    let next = BuildIdentity(path: "/i/builds/bbb/KeepgoingMenu", stamp: "1-2", label: "bbb")
    var store = MemoryStore()
    var env = FakeEnvironment()
    var flow: UpdateFlow!

    override func setUp() {
        store = MemoryStore()
        env = FakeEnvironment()
        flow = UpdateFlow(updater: MenuUpdater(running: running, store: store), launchPath: running.path, env: env)
    }

    // MARK: checking

    func testNothingNewMeansNothingHappens() {
        flow.check()
        XCTAssertNil(flow.installed)
        XCTAssertEqual(env.children.count, 0)
        XCTAssertEqual(env.available, [])
    }

    func testAnAutomaticUpdateStartsTheNewMenuAndQuitsOnlyAfterItHasStayedUp() {
        env.state = .newer(next)
        flow.check()
        XCTAssertEqual(env.children.count, 1)
        XCTAssertEqual(env.quit_, 0, "not at once")
        XCTAssertEqual(env.available, [], "with automatic on, 'available' is not said")
        XCTAssertEqual(store.values["updateOldPID"], "100")
        env.advance(UpdateFlow.settle)
        XCTAssertEqual(env.quit_, 1)
        XCTAssertTrue(flow.updater.beginAttempt(next) == false, "the one attempt was used")
    }

    func testBusyOrOpenHoldsAnAutomaticUpdateBackAtNoCost() {
        env.state = .newer(next)
        env.menuOpen = true
        flow.check()
        XCTAssertEqual(env.children.count, 0)
        env.menuOpen = false
        let work = flow.busy.begin()
        flow.check()
        XCTAssertEqual(env.children.count, 0)
        flow.busy.end(work)
        flow.check()
        XCTAssertEqual(env.children.count, 1, "held back used no attempt")
    }

    func testAConfirmationWindowAndThePanelRestartCountAsWork() {
        env.state = .newer(next)
        let review = flow.busy.begin()   // review(): from the read through the open window
        flow.check()
        XCTAssertEqual(env.children.count, 0)
        flow.busy.end(review)
        let panel = flow.busy.begin()    // openWithControls(): the stop, the start
        flow.check()
        XCTAssertEqual(env.children.count, 0)
        flow.busy.end(panel)
    }

    func testBecomingBusyDuringTheSignatureCheckPostponesAndGivesTheAttemptBack() {
        env.state = .newer(next)
        env.verifyFinishesLater = true
        flow.check()
        XCTAssertTrue(flow.relaunching)
        let work = flow.busy.begin()   // a review starts while codesign runs
        env.pendingVerify.removeFirst()()
        XCTAssertEqual(env.children.count, 0)
        XCTAssertFalse(flow.relaunching)
        flow.busy.end(work)
        env.verifyFinishesLater = false
        flow.check()
        XCTAssertEqual(env.children.count, 1, "the build's attempt was given back")
    }

    func testTheMenuOpenedDuringTheSettleStopsTheNewMenuAndKeepsThisOne() {
        env.state = .newer(next)
        flow.check()
        env.menuOpen = true
        env.advance(UpdateFlow.settle)
        XCTAssertEqual(env.quit_, 0)
        XCTAssertEqual(env.children[0].terminated, 1)
        XCTAssertFalse(env.children[0].isRunning)
        XCTAssertFalse(flow.relaunching)
        XCTAssertNil(store.values["updateOldPID"])
        XCTAssertNil(store.values["updateRelaunchedTo"], "nothing is left to claim an update")
        env.menuOpen = false
        flow.check()
        XCTAssertEqual(env.children.count, 2, "tried again later")
    }

    func testAStoppedNewMenuThatIgnoresTheSignalIsKilledAndThenGivenUpOn() {
        env.state = .newer(next)
        flow.check()
        env.menuOpen = true
        env.children[0].exitsOnTerminate = false
        env.advance(UpdateFlow.settle)
        env.advance(UpdateFlow.stopGrace + 0.5)
        XCTAssertEqual(env.children[0].killed, 1, "escalated")
        XCTAssertFalse(env.children[0].isRunning)
        XCTAssertFalse(flow.relaunching)
        // A child that survives even that is said out loud, never left silently.
        let stubborn = FakeEnvironment()
        let f = UpdateFlow(updater: MenuUpdater(running: running, store: MemoryStore()), launchPath: running.path, env: stubborn)
        stubborn.state = .newer(next)
        f.check()
        stubborn.menuOpen = true
        stubborn.children[0].exitsOnTerminate = false
        stubborn.children[0].exitsOnKill = false
        stubborn.advance(UpdateFlow.settle)
        stubborn.advance(UpdateFlow.stopGrace * 2 + 1)
        XCTAssertEqual(stubborn.told.first?.0, "Two menus are running")
        XCTAssertEqual(stubborn.quit_, 0)
    }

    func testNoAttemptIsMadeWhileAStoppedChildIsStillBeingConfirmed() {
        env.state = .newer(next)
        flow.check()
        env.menuOpen = true
        env.children[0].exitsOnTerminate = false
        env.advance(UpdateFlow.settle + 0.5)
        XCTAssertTrue(flow.relaunching, "still confirming")
        env.menuOpen = false
        flow.check()
        XCTAssertEqual(env.children.count, 1, "no third menu")
    }

    // MARK: failures

    func testACheckThatFailsKeepsTheMenuRunningAndSaysWhyOnceForEachBuild() {
        env.state = .newer(next)
        env.verifyResult = RelaunchFailure("the new app has no readable Info.plist")
        flow.check()
        XCTAssertEqual(env.children.count, 0)
        XCTAssertEqual(env.notices.last, "Update not started: the new app has no readable Info.plist.")
        XCTAssertEqual(flow.updater.lastFailure, "the new app has no readable Info.plist")
        env.verifyResult = nil
        flow.check()
        XCTAssertEqual(env.children.count, 0, "never loops")
        XCTAssertEqual(env.available, ["looptender menu: update available (bbb)"], "said once, after the attempt failed")
        flow.check()
        XCTAssertEqual(env.available.count, 1)
    }

    func testAStartThatFailsAndANewMenuThatExitsAtOnce() {
        env.state = .newer(next)
        struct Boom: Error {}
        env.startError = Boom()
        flow.check()
        XCTAssertTrue(env.notices.last?.hasPrefix("Update not started: the new menu could not be started") == true)
        XCTAssertNil(store.values["updateOldPID"])
        let other = FakeEnvironment()
        let f = UpdateFlow(updater: MenuUpdater(running: running, store: MemoryStore()), launchPath: running.path, env: other)
        other.state = .newer(next)
        f.check()
        other.children[0].isRunning = false
        other.children[0].exitStatus = 7
        other.advance(UpdateFlow.settle)
        XCTAssertEqual(other.quit_, 0)
        XCTAssertEqual(other.notices.last, "Update not started: the new menu exited at once (status 7).")
    }

    // MARK: a person's own request

    func testARequestWhileWorkIsOpenIsRefusedOutLoud() {
        env.state = .newer(next)
        env.menuOpen = true       // the menu is open when the item is chosen
        flow.check()
        let work = flow.busy.begin()
        flow.relaunch(byRequest: true)
        XCTAssertEqual(env.children.count, 0)
        XCTAssertEqual(env.told.last?.0, "The menu was not updated")
        XCTAssertEqual(env.told.last?.1, RelaunchGate.busyMessage)
        flow.busy.end(work)
        flow.relaunch(byRequest: true)
        XCTAssertEqual(env.children.count, 1, "an open menu does not stop a chosen update")
    }

    func testARequestWhoseFileChangedIsToldNotSilentlyDropped() {
        env.state = .newer(next)
        env.menuOpen = true
        flow.check()
        env.verifyFinishesLater = true
        flow.relaunch(byRequest: true)
        env.state = .newer(BuildIdentity(path: next.path, stamp: "9-9", label: "bbb"))
        env.pendingVerify.removeFirst()()
        XCTAssertEqual(env.children.count, 0)
        XCTAssertEqual(env.told.last?.0, "The menu was not updated")
        XCTAssertTrue(env.told.last?.1.contains("changed") == true)
        XCTAssertEqual(flow.installed?.stamp, "9-9", "and the new identity is the one offered next")
        // The same for work that began meanwhile.
        env.verifyFinishesLater = true
        flow.relaunch(byRequest: true)
        let work = flow.busy.begin()
        env.pendingVerify.removeFirst()()
        XCTAssertEqual(env.told.last?.1, RelaunchGate.busyMessage)
        flow.busy.end(work)
    }

    func testARequestedFailureIsAnAlertAndAnAutomaticOneIsNot() {
        env.state = .newer(next)
        env.menuOpen = true
        flow.check()
        env.verifyResult = RelaunchFailure("the new menu program is missing or not runnable")
        flow.relaunch(byRequest: true)
        XCTAssertEqual(env.told.last?.0, "The menu could not be updated")
    }

    // MARK: saying "updated" only when it is true

    func testTheNewMenuSaysUpdatedOnlyAfterTheOldOneHasExited() {
        // The old menu (pid 100) started the new one: it recorded itself and the claim.
        let oldStore = MemoryStore()
        let oldUpdater = MenuUpdater(running: running, store: oldStore)
        XCTAssertTrue(oldUpdater.beginAttempt(next))
        oldUpdater.handOverPID(100)
        let newEnv = FakeEnvironment()
        newEnv.processID = 200
        newEnv.alive = [100]
        let newFlow = UpdateFlow(updater: MenuUpdater(running: next, store: oldStore), launchPath: next.path, env: newEnv)
        newFlow.confirmUpdateAfterStart()
        newEnv.advance(5)
        XCTAssertEqual(newEnv.updated, [], "the old menu is still running")
        newEnv.alive = []
        newEnv.advance(1)
        XCTAssertEqual(newEnv.updated, ["looptender menu updated to bbb"])
        newEnv.advance(30)
        XCTAssertEqual(newEnv.updated.count, 1, "once")
        XCTAssertNil(oldStore.values["updateRelaunchedTo"])
    }

    func testAnOldMenuThatNeverExitsMeansNothingIsSaid() {
        let store = MemoryStore()
        let oldUpdater = MenuUpdater(running: running, store: store)
        XCTAssertTrue(oldUpdater.beginAttempt(next))
        oldUpdater.handOverPID(100)
        let newEnv = FakeEnvironment()
        newEnv.processID = 200
        newEnv.alive = [100]
        let newFlow = UpdateFlow(updater: MenuUpdater(running: next, store: store), launchPath: next.path, env: newEnv)
        newFlow.confirmUpdateAfterStart()
        newEnv.advance(UpdateFlow.handOverWait + 1)
        XCTAssertEqual(newEnv.updated, [])
        XCTAssertNil(store.values["updateRelaunchedTo"], "and the claim is dropped, so it is not said later either")
    }

    func testAnAbortedRelaunchNeverLeavesAClaimForALaterStart() {
        env.state = .newer(next)
        flow.check()
        env.menuOpen = true
        env.advance(UpdateFlow.settle)   // aborted
        // A later start of the build that was aborted finds no claim.
        XCTAssertNil(MenuUpdater(running: next, store: store).pendingUpdate())
    }
}
