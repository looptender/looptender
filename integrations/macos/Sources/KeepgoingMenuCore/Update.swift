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
    static let attemptedKey = "updateAttemptedBuild"
    static let relaunchedKey = "updateRelaunchedTo"

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

    /// What to do, given the installed build read now. `menuOpen` and
    /// `controlInFlight` hold back an automatic relaunch only. A relaunch is
    /// attempted once for each installed build, so a failing one never loops;
    /// the person's own choice (`relaunchByRequest`) is not counted.
    public func step(installed: BuildIdentity?, menuOpen: Bool, controlInFlight: Bool) -> UpdateStep {
        guard let installed, installed.id != running.id else { return .nothing }
        let attempted = store.string(forKey: Self.attemptedKey) == installed.id
        if automatic, !attempted {
            if menuOpen || controlInFlight { return .offer(label: installed.label, notify: false) }
            // Marked before anything starts, so a failure cannot repeat.
            store.set(installed.id, forKey: Self.attemptedKey)
            store.set(installed.id, forKey: Self.relaunchedKey)
            return .relaunch(label: installed.label)
        }
        // Said once for each build; with automatic updating on, only after its
        // attempt failed (otherwise "updated" is what is said, once, after).
        let notify = store.string(forKey: Self.notifiedKey) != installed.id
        if notify { store.set(installed.id, forKey: Self.notifiedKey) }
        return .offer(label: installed.label, notify: notify)
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

    /// Said once, by the menu that an automatic relaunch started, about its
    /// own build; nil otherwise.
    public func updatedNotice() -> String? {
        guard store.string(forKey: Self.relaunchedKey) == running.id else { return nil }
        store.set(nil, forKey: Self.relaunchedKey)
        return "looptender menu updated to \(running.label)"
    }

    public static func availableNotice(_ label: String) -> String { "looptender menu: update available (\(label))" }
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
