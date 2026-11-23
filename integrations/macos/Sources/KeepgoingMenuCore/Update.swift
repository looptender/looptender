import Foundation

// The menu updating itself to the installed build. Everything here is local:
// it compares the build the menu is running with the build the install now
// points at, and never asks the network for anything.

/// One build of the menu: where its executable really is, a stamp that
/// changes when that file is replaced, and the name shown for it.
public struct BuildIdentity: Equatable {
    public let path: String
    public let stamp: String
    public let label: String
    public init(path: String, stamp: String, label: String) {
        self.path = path
        self.stamp = stamp
        self.label = label
    }
    /// What two identities are compared by.
    public var id: String { path + "#" + stamp }
}

/// Finds the build the install now points at, from the path the menu was
/// launched through. Reads the file system only.
public enum InstalledBuild {
    /// The identity of the executable at `path`, links resolved; nil when it
    /// is not there.
    public static func identity(of path: String, fileManager: FileManager = .default) -> BuildIdentity? {
        let real = URL(fileURLWithPath: path).resolvingSymlinksInPath().path
        guard let attrs = try? fileManager.attributesOfItem(atPath: real), (attrs[.type] as? FileAttributeType) == .typeRegular else { return nil }
        let size = (attrs[.size] as? NSNumber)?.intValue ?? 0
        let time = (attrs[.modificationDate] as? Date)?.timeIntervalSince1970 ?? 0
        return BuildIdentity(path: real, stamp: "\(size)-\(Int(time))", label: label(forRealPath: real))
    }

    /// The name shown: the directory under `builds/` the executable lives in
    /// (`builds/0b4e12399667/...` is `0b4e12399667`), else the version in the
    /// enclosing app's Info.plist, else the folder the app is in.
    static func label(forRealPath real: String) -> String {
        let parts = real.split(separator: "/").map(String.init)
        if let i = parts.lastIndex(of: "builds"), i + 1 < parts.count - 1 { return parts[i + 1] }
        if let i = parts.lastIndex(where: { $0.hasSuffix(".app") }) {
            let plist = "/" + parts[0...i].joined(separator: "/") + "/Contents/Info.plist"
            if let info = NSDictionary(contentsOfFile: plist), let v = info["CFBundleShortVersionString"] as? String, !v.isEmpty { return v }
        }
        return "the installed build"
    }

    /// What the install says now. `noMenu` is the layout rule finding that
    /// `<root>/current` leads to a build that ships no menu app there.
    public enum State: Equatable {
        case same
        case newer(BuildIdentity)
        case noMenu(label: String)
    }

    /// `current`, with the case where the install moved to a build that has no
    /// menu app told apart from nothing having changed.
    public static func state(launchPath: String, running: BuildIdentity, fileManager: FileManager = .default) -> State {
        if let found = current(launchPath: launchPath, running: running, fileManager: fileManager) { return .newer(found) }
        let parts = running.path.split(separator: "/").map(String.init)
        guard let i = parts.lastIndex(of: "builds"), i + 2 < parts.count else { return .same }
        let root = "/" + parts[0..<i].joined(separator: "/")
        let link = root == "/" ? "/current" : root + "/current"
        let target = URL(fileURLWithPath: link).resolvingSymlinksInPath().path
        let mine = "/" + parts[0...(i + 1)].joined(separator: "/")
        var isDir: ObjCBool = false
        guard target != mine, fileManager.fileExists(atPath: target, isDirectory: &isDir), isDir.boolValue else { return .same }
        let rest = parts[(i + 2)...].joined(separator: "/")
        guard identity(of: target + "/" + rest, fileManager: fileManager) == nil else { return .same }
        return .noMenu(label: label(forRealPath: target + "/" + rest))
    }

    /// The build installed now, for a menu running `running` that was
    /// started as `launchPath` (the path in argv[0], made absolute, links not
    /// resolved). Two ways the install can move, each only a file system read:
    /// the path it was launched through now leads somewhere else or to a
    /// replaced file, or the run is under `<root>/builds/<id>/...` and
    /// `<root>/current` now leads to another build. nil when neither says
    /// there is anything else.
    public static func current(launchPath: String, running: BuildIdentity, fileManager: FileManager = .default) -> BuildIdentity? {
        var candidates: [String] = [launchPath]
        let parts = running.path.split(separator: "/").map(String.init)
        if let i = parts.lastIndex(of: "builds"), i + 2 < parts.count {
            let root = "/" + parts[0..<i].joined(separator: "/")
            let rest = parts[(i + 2)...].joined(separator: "/")
            let link = root == "/" ? "/current" : root + "/current"
            let target = URL(fileURLWithPath: link).resolvingSymlinksInPath().path
            candidates.append(target + "/" + rest)
        }
        for candidate in candidates {
            if let found = identity(of: candidate, fileManager: fileManager), found.id != running.id { return found }
        }
        return nil
    }
}

/// A place to keep what must survive a relaunch: one value per key.
public protocol UpdateStore: AnyObject {
    func string(forKey key: String) -> String?
    func set(_ value: String?, forKey key: String)
}

/// Why a relaunch was not started. Nothing was quit.
public struct RelaunchFailure: Error, Equatable {
    public let reason: String
    public init(_ reason: String) { self.reason = reason }
}

/// What the app should do now about an installed build that differs.
public enum UpdateStep: Equatable {
    case nothing
    /// Mark the icon and, once for this build, say so.
    case offer(label: String, notify: Bool)
    /// Relaunch now, by itself.
    case relaunch(label: String)
}

/// Decides what the menu does about a newer install, and remembers, across
/// relaunches, what it has already done for each installed build.
public final class MenuUpdater {
    static let autoKey = "updateAutomatically"
    static let notifiedKey = "updateNotifiedBuild"
    static let attemptsKey = "updateAttempts"
    static let relaunchedKey = "updateRelaunchedTo"
    static let toldKey = "updateToldGaps"
    static let oldPIDKey = "updateOldPID"
    /// Automatic attempts in any hour, whatever the builds: a path rewritten in
    /// place gives each rewrite a new identity, and this stops that being
    /// unlimited.
    public static let maxAttemptsPerHour = 3

    public let running: BuildIdentity
    private let store: UpdateStore
    /// Why the last attempt for this build failed, shown in the menu.
    public private(set) var lastFailure: String?

    public init(running: BuildIdentity, store: UpdateStore) {
        self.running = running
        self.store = store
    }

    /// "Update the menu automatically": on unless a person turned it off.
    public var automatic: Bool {
        get { store.string(forKey: Self.autoKey) != "off" }
        set { store.set(newValue ? nil : "off", forKey: Self.autoKey) }
    }

    private struct Attempt: Codable { let id: String; let at: TimeInterval }
    private func attempts(now: Date) -> [Attempt] {
        guard let raw = store.string(forKey: Self.attemptsKey), let list = try? JSONDecoder().decode([Attempt].self, from: Data(raw.utf8)) else { return [] }
        return list
    }
    private func save(_ list: [Attempt]) {
        let data = try? JSONEncoder().encode(list)
        store.set(list.isEmpty ? nil : data.flatMap { String(data: $0, encoding: .utf8) }, forKey: Self.attemptsKey)
    }
    private func mayAttempt(_ installed: BuildIdentity, now: Date) -> Bool {
        let list = attempts(now: now)
        return !list.contains { $0.id == installed.id } && list.filter { now.timeIntervalSince1970 - $0.at < 3600 }.count < Self.maxAttemptsPerHour
    }

    /// What to do, given the installed build read now. `menuOpen` and
    /// `controlInFlight` hold back an automatic relaunch only; nothing is
    /// recorded until `beginAttempt`, so being held back costs nothing. Each
    /// installed build is attempted once, and at most `maxAttemptsPerHour`
    /// in any hour, so a failing one never loops; the person's own choice is
    /// not counted.
    public func step(installed: BuildIdentity?, menuOpen: Bool, controlInFlight: Bool, now: Date = Date()) -> UpdateStep {
        guard let installed, installed.id != running.id else { return .nothing }
        if automatic, mayAttempt(installed, now: now) {
            if menuOpen || controlInFlight { return .offer(label: installed.label, notify: false) }
            return .relaunch(label: installed.label)
        }
        // Said once for each build; with automatic updating on, only after its
        // attempt failed (otherwise "updated" is what is said, once, after).
        let notify = store.string(forKey: Self.notifiedKey) != installed.id
        if notify { store.set(installed.id, forKey: Self.notifiedKey) }
        return .offer(label: installed.label, notify: notify)
    }

    /// Records an automatic attempt, just before anything is started, so a
    /// failure cannot repeat. False when this build was tried or the hour's
    /// cap is reached.
    public func beginAttempt(_ installed: BuildIdentity, now: Date = Date()) -> Bool {
        guard mayAttempt(installed, now: now) else { return false }
        save(attempts(now: now).filter { now.timeIntervalSince1970 - $0.at < 3600 } + [Attempt(id: installed.id, at: now.timeIntervalSince1970)])
        store.set(installed.id, forKey: Self.relaunchedKey)
        return true
    }

    /// An attempt that never started anything (the menu got busy first) is
    /// given back: it does not use the build's one attempt.
    public func postponeAttempt(_ installed: BuildIdentity) {
        save(attempts(now: Date()).filter { $0.id != installed.id })
        store.set(nil, forKey: Self.relaunchedKey)
    }

    /// The relaunch the person chose. It is not limited to one attempt, and
    /// is not an automatic one, so no "updated" notice follows it.
    public func relaunchByRequest(installed: BuildIdentity) {
        store.set(nil, forKey: Self.relaunchedKey)
    }

    /// The start of an automatic relaunch failed: nothing was quit.
    public func attemptFailed(_ failure: RelaunchFailure) {
        lastFailure = failure.reason
        store.set(nil, forKey: Self.relaunchedKey)
    }

    /// What the new menu may say about an automatic relaunch that started it,
    /// and the process it replaces. Not consumed until `finishUpdate`, which
    /// the caller runs only once that process has exited.
    public struct PendingUpdate: Equatable {
        public let text: String
        public let oldPID: Int32?
    }
    public func pendingUpdate() -> PendingUpdate? {
        guard store.string(forKey: Self.relaunchedKey) == running.id else { return nil }
        return PendingUpdate(text: "looptender menu updated to \(running.label)", oldPID: store.string(forKey: Self.oldPIDKey).flatMap { Int32($0) })
    }
    public func finishUpdate() {
        store.set(nil, forKey: Self.relaunchedKey)
        store.set(nil, forKey: Self.oldPIDKey)
    }
    /// The process that is about to be replaced, written before the new menu
    /// starts; nil clears it.
    public func handOverPID(_ pid: Int32?) { store.set(pid.map(String.init), forKey: Self.oldPIDKey) }

    public static func availableNotice(_ label: String) -> String { "looptender menu: update available (\(label))" }

    /// The checks-have-stopped notices already told, handed to the menu that
    /// replaces this one so it does not tell them again. Taken once.
    public func handOverTold(_ told: String?) { store.set(told, forKey: Self.toldKey) }
    public func takeTold() -> String? {
        defer { store.set(nil, forKey: Self.toldKey) }
        return store.string(forKey: Self.toldKey)
    }
}

/// Work the menu must not quit under: reads, confirmation windows and
/// changes being made. Each piece of work holds a token from `begin` to
/// `end`, so nothing is counted twice or released twice.
public final class BusyCounter {
    private var open: Set<Int> = []
    private var next = 0
    public init() {}
    public func begin() -> Int {
        next += 1
        open.insert(next)
        return next
    }
    /// Releasing a token twice, or one never issued, changes nothing.
    public func end(_ token: Int) { open.remove(token) }
    public var count: Int { open.count }
    /// Whether the menu may not quit now: it is open, or work is in flight.
    public func isBusy(menuOpen: Bool) -> Bool { menuOpen || !open.isEmpty }
}

/// The two moments a relaunch re-asks whether it may go on. Each is decided
/// from what is true then, not from what was true when it was chosen.
public enum RelaunchGate {
    public static let busyMessage = "Finish or cancel the open action first; the menu will not quit under it."

    public enum Start: Equatable { case go, busy, changed }
    /// Just before the new menu is started, after the checks: still idle, and
    /// the file about to run is the one that was checked (same real path, size
    /// and modification time).
    public static func beforeStart(busy: Bool, checked: BuildIdentity, now: BuildIdentity?) -> Start {
        if busy { return .busy }
        return now?.id == checked.id ? .go : .changed
    }

    public enum Quit: Equatable { case quit, abort, failed(String) }
    /// When the new menu has had a moment to start: quit only if it is still
    /// running and the menu is still idle. `abort` means the new menu is
    /// stopped and this one keeps running, nothing counted against the build.
    public static func beforeQuit(busy: Bool, newMenuRunning: Bool, exitStatus: Int32) -> Quit {
        if !newMenuRunning { return .failed("the new menu exited at once (status \(exitStatus))") }
        return busy ? .abort : .quit
    }
}

/// Checks a build before the menu quits for it. Each check is something that
/// can be wrong with a half-copied or unsigned install; nothing runs the new
/// build yet.
public enum RelaunchCheck {
    /// `verifySignature` runs the platform's signature check for a bundle (it
    /// is a parameter so tests need no signed app).
    public static func verify(_ build: BuildIdentity, runningPath: String, fileManager: FileManager = .default, verifySignature: (String) -> String?) -> RelaunchFailure? {
        guard fileManager.isExecutableFile(atPath: build.path) else { return RelaunchFailure("the new menu program is missing or not runnable") }
        let parts = build.path.split(separator: "/").map(String.init)
        if let i = parts.lastIndex(where: { $0.hasSuffix(".app") }) {
            let bundle = "/" + parts[0...i].joined(separator: "/")
            guard let info = NSDictionary(contentsOfFile: bundle + "/Contents/Info.plist") else { return RelaunchFailure("the new app has no readable Info.plist") }
            if let exec = info["CFBundleExecutable"] as? String, exec != parts.last { return RelaunchFailure("the new app's Info.plist names a different program") }
            let runParts = runningPath.split(separator: "/").map(String.init)
            if let j = runParts.lastIndex(where: { $0.hasSuffix(".app") }),
               let mine = NSDictionary(contentsOfFile: "/" + runParts[0...j].joined(separator: "/") + "/Contents/Info.plist"),
               let a = mine["CFBundleIdentifier"] as? String, let b = info["CFBundleIdentifier"] as? String, a != b {
                return RelaunchFailure("the new app has a different bundle identifier")
            }
            if let why = verifySignature(bundle) { return RelaunchFailure("the new app's signature does not verify: \(why)") }
        }
        return nil
    }
}
