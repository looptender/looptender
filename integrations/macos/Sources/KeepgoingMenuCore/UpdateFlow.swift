import Foundation

// The menu's whole update behaviour, with everything that touches the real
// app (windows, processes, timers, notifications) behind `UpdateEnvironment`,
// so the decisions and their order can be tested without the app.

/// The menu that a relaunch started.
public protocol RunningMenu: AnyObject {
    var isRunning: Bool { get }
    var exitStatus: Int32 { get }
    /// A polite stop (SIGTERM).
    func terminate()
    /// A forced stop (SIGKILL).
    func kill()
}

public protocol UpdateEnvironment: AnyObject {
    var menuOpen: Bool { get }
    /// What the install says now (file system only).
    func installedState() -> InstalledBuild.State
    /// Runs the signature and file checks off the main thread and calls back on it.
    func verify(_ target: BuildIdentity, runningPath: String, completion: @escaping (RelaunchFailure?) -> Void)
    func start(_ target: BuildIdentity) throws -> RunningMenu
    func after(_ seconds: TimeInterval, _ work: @escaping () -> Void)
    func quit()
    func processAlive(_ pid: Int32) -> Bool
    var processID: Int32 { get }
    func postAvailable(_ text: String)
    func postUpdated(_ text: String)
    /// A dialog, for what the person asked for.
    func tell(_ title: String, _ text: String)
    /// A line in the menu for one opening.
    func setNotice(_ text: String)
    func exportTold() -> String
    /// The model changed: draw it again.
    func changed()
}

public final class UpdateFlow {
    /// How long the new menu must stay up before this one quits.
    public static let settle: TimeInterval = 1.5
    /// How long a stopped new menu has to exit before it is killed, and then
    /// before it is given up on.
    public static let stopGrace: TimeInterval = 2
    /// How long the new menu waits for the old one to exit before it says nothing.
    public static let handOverWait: TimeInterval = 15
    static let poll: TimeInterval = 0.25

    public let updater: MenuUpdater
    public let busy = BusyCounter()
    public let launchPath: String
    public private(set) var installed: BuildIdentity?
    public private(set) var noMenuLabel: String?
    public private(set) var relaunching = false
    private unowned let env: UpdateEnvironment

    public init(updater: MenuUpdater, launchPath: String, env: UpdateEnvironment) {
        self.updater = updater
        self.launchPath = launchPath
        self.env = env
    }

    private func idle(byRequest: Bool) -> Bool { !busy.isBusy(menuOpen: byRequest ? false : env.menuOpen) }

    /// Reads the install again and does what the updater says: remember what
    /// is waiting, say so once for each installed build, and, with automatic
    /// updating on and the menu closed and idle, relaunch.
    public func check() {
        guard !relaunching else { return }
        switch env.installedState() {
        case .same: installed = nil; noMenuLabel = nil
        case .noMenu(let label): installed = nil; noMenuLabel = label
        case .newer(let found): installed = found; noMenuLabel = nil
        }
        switch updater.step(installed: installed, menuOpen: env.menuOpen, controlInFlight: busy.count > 0) {
        case .nothing: break
        case .offer(let label, let notify):
            if notify { env.postAvailable(MenuUpdater.availableNotice(label)) }
        case .relaunch:
            relaunch(byRequest: false)
        }
        env.changed()
    }

    /// The menu has started: say "updated" only once the menu it replaced has
    /// really exited, so an update that was aborted is never announced.
    public func confirmUpdateAfterStart() {
        guard let pending = updater.pendingUpdate() else { return }
        guard let old = pending.oldPID, old != env.processID else {
            env.postUpdated(pending.text)
            updater.finishUpdate()
            return
        }
        var waited: TimeInterval = 0
        func poll() {
            if !env.processAlive(old) {
                env.postUpdated(pending.text)
                updater.finishUpdate()
            } else if waited >= Self.handOverWait {
                updater.finishUpdate() // the old menu kept running: nothing was replaced
            } else {
                waited += Self.poll
                env.after(Self.poll, poll)
            }
        }
        poll()
    }

    /// Starts the installed build with the arguments this one was given, and
    /// quits only when the menu is still idle and the new one is running. Each
    /// gate asks again at its own moment (RelaunchGate); if anything is wrong
    /// this menu keeps running and says why, and a person who asked is told.
    /// A relaunch held back because the menu got busy costs no attempt.
    public func relaunch(byRequest: Bool) {
        guard !relaunching, let target = installed else { return }
        if !idle(byRequest: byRequest) {
            // A chosen update never quits under open work; an automatic one waits for the next look.
            if byRequest, busy.count > 0 { env.tell("The menu was not updated", RelaunchGate.busyMessage) }
            return
        }
        if byRequest {
            updater.relaunchByRequest(installed: target)
        } else if !updater.beginAttempt(target) {
            return
        }
        relaunching = true
        func fail(_ reason: String) {
            relaunching = false
            if !byRequest { updater.attemptFailed(RelaunchFailure(reason)) }
            updater.handOverPID(nil)
            env.setNotice("Update not started: " + reason + ".")
            env.changed()
            if byRequest { env.tell("The menu could not be updated", "Nothing was changed and this menu keeps running: " + reason + ".") }
        }
        func postpone(_ why: String? = nil) {
            relaunching = false
            if !byRequest { updater.postponeAttempt(target) }
            updater.handOverPID(nil)
            env.changed()
            if byRequest, let why { env.tell("The menu was not updated", why) }
        }
        env.verify(target, runningPath: updater.running.path) { [self] problem in
            if let problem { fail(problem.reason); return }
            // The file about to run must be the one that was checked, and nothing may have started meanwhile.
            var now: BuildIdentity?
            if case .newer(let found) = env.installedState() { now = found }
            switch RelaunchGate.beforeStart(busy: !idle(byRequest: byRequest), checked: target, now: now) {
            case .busy: postpone(byRequest ? RelaunchGate.busyMessage : nil); return
            case .changed:
                installed = now
                postpone("The installed build changed while it was being checked. Nothing was changed; choose the update again.")
                return
            case .go: break
            }
            updater.handOverTold(env.exportTold())
            updater.handOverPID(env.processID)
            let child: RunningMenu
            do { child = try env.start(target) } catch {
                updater.handOverTold(nil)
                fail("the new menu could not be started (\(error.localizedDescription))")
                return
            }
            // Quit only if the new one has stayed up and the menu is still idle.
            env.after(Self.settle) { [self] in
                switch RelaunchGate.beforeQuit(busy: !idle(byRequest: byRequest), newMenuRunning: child.isRunning, exitStatus: child.exitStatus) {
                case .quit:
                    env.quit()
                case .abort:
                    // Busy again: stop the new menu, and give the attempt back only once it is gone.
                    updater.handOverTold(nil)
                    stop(child) { [self] gone in
                        if gone {
                            postpone()
                        } else {
                            relaunching = false
                            updater.handOverPID(nil)
                            env.setNotice("The new menu could not be stopped; quit it from Activity Monitor.")
                            env.changed()
                            env.tell("Two menus are running", "The update was postponed because the menu was busy, but the new menu did not stop. Quit one of the two looptender menus from Activity Monitor.")
                        }
                    }
                case .failed(let reason):
                    updater.handOverTold(nil)
                    fail(reason)
                }
            }
        }
    }

    /// Stops the new menu and confirms it: a polite stop, then a forced one
    /// after `stopGrace`, then giving up after another `stopGrace`.
    func stop(_ child: RunningMenu, _ done: @escaping (Bool) -> Void) {
        child.terminate()
        var waited: TimeInterval = 0
        var killed = false
        func poll() {
            if !child.isRunning { done(true); return }
            if waited >= Self.stopGrace * 2 { done(false); return }
            if waited >= Self.stopGrace, !killed { killed = true; child.kill() }
            waited += Self.poll
            env.after(Self.poll, poll)
        }
        poll()
    }
}
