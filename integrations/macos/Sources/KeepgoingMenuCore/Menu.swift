import Foundation

// The menu's contents, decided here so they can be tested without AppKit.
// Every row except the last opens the panel, opens a loop's choices, stops
// the panel's page or quits; the last row is the single dimmed status line.

/// One loop as `keepgoing panel list --json` reports it. The phrase is the
/// panel page's own wording; this file never invents a state.
public struct PanelLoop: Decodable, Equatable {
    public let key: String
    public let name: String
    public let state: String
    public let phrase: String
    public let needsYou: Bool
    /// The panel's no-progress sentence when that flag is raised, absent
    /// otherwise. It informs and is never a need: it changes no badge.
    public let noProgress: String?
    /// Which changes fit the loop's control flags, as the page offers them:
    /// pause, stop, resume. Absent from a CLI that has no client review.
    public var can: [String]? = nil
    /// The time of the loop's last check, when it needs a person because its
    /// checks have stopped. See OverdueNotices.
    public let overdueSince: String?
    /// The build the loop's supervisor last recorded running, shortened by
    /// the CLI. Absent when none is recorded, when it has no version, and
    /// from a CLI that predates it; the menu then says nothing about it.
    public var recordedBuild: String? = nil
    /// Set when that build is a different one from the installed CLI. It
    /// never says which is newer.
    public var buildDiffers: Bool? = nil
    /// The configured provider and model of the loop's current target, shown
    /// beside its state so the name is not the only cue. Absent from a CLI
    /// that predates them.
    public var provider: String? = nil
    public var model: String? = nil
    /// The loop's supervisor build against the installed one, in the CLI's
    /// words (the panel shows the same). Absent from a CLI that predates it
    /// and when nothing can be said; the menu then says nothing.
    public var buildFreshness: PanelFreshness? = nil
    /// The loop's one display name, as the panel shows it (the stable name,
    /// without a model or a generation). Absent from a CLI that predates it:
    /// `name` is then the registered name.
    public var naming: PanelNaming? = nil
    /// Set while the loop is archived. An archived loop is out of the way:
    /// it never counts as needing a person and is listed last.
    public var archived: PanelArchived? = nil
    /// When the loop's last check ran, and what it waits for in the page's
    /// fixed words (never the hold's note).
    public var checkedAt: String? = nil
    /// Saved meaningful loop work/movement; never the list/check read time.
    public var activityAt: String? = nil
    public var reason: String? = nil
    /// What it waits for, by kind: asked, held, limit, checks or unknown.
    public var waiting: PanelWaiting? = nil

    /// Set when the loop was replaced by a successor: out of the way,
    /// whatever hold it kept. Absent from a CLI that predates it.
    public var superseded: Bool? = nil
    /// Set when the loop's target is an observed sessionpipe session:
    /// looptender never sends to, wakes, re-prompts or stops it unless
    /// observedWakes is true (woken only when idle).
    public var observed: Bool? = nil
    public var observedWakes: Bool? = nil
    /// Set when the loop has had no check activity for at least 3 days (72h).
    public var inactive: Bool? = nil

    public init(key: String, name: String, state: String, phrase: String, needsYou: Bool, noProgress: String? = nil, can: [String]? = nil, overdueSince: String? = nil, recordedBuild: String? = nil, buildDiffers: Bool? = nil, provider: String? = nil, model: String? = nil, buildFreshness: PanelFreshness? = nil, naming: PanelNaming? = nil, archived: PanelArchived? = nil, checkedAt: String? = nil, reason: String? = nil, waiting: PanelWaiting? = nil, superseded: Bool? = nil, observed: Bool? = nil, observedWakes: Bool? = nil, inactive: Bool? = nil, activityAt: String? = nil) {
        self.observed = observed
        self.observedWakes = observedWakes
        self.inactive = inactive
        self.activityAt = activityAt
        self.superseded = superseded
        self.waiting = waiting
        self.naming = naming
        self.archived = archived
        self.checkedAt = checkedAt
        self.reason = reason
        self.buildFreshness = buildFreshness
        self.key = key
        self.name = name
        self.state = state
        self.phrase = phrase
        self.needsYou = needsYou
        self.noProgress = noProgress
        self.can = can
        self.overdueSince = overdueSince
        self.recordedBuild = recordedBuild
        self.buildDiffers = buildDiffers
        self.provider = provider
        self.model = model
    }
}

/// A loop's display name, already masked by the CLI.
public struct PanelNaming: Decodable, Equatable {
    public let displayName: String
    /// The display name cut at its end by the CLI's own rule; absent when it is the name itself.
    public var shortName: String? = nil
    public init(displayName: String, shortName: String? = nil) { self.displayName = displayName; self.shortName = shortName }
}

/// What a loop waits for, by kind only: never the hold's note.
public struct PanelWaiting: Decodable, Equatable {
    public let kind: String
    public var by: String? = nil
    public init(kind: String, by: String? = nil) { self.kind = kind; self.by = by }
}

/// An archived loop: when. Its note is never carried.
public struct PanelArchived: Decodable, Equatable {
    public var at: String? = nil
    public init(at: String? = nil) { self.at = at }
}

/// How fresh one supervisor's build is. The words are the CLI's; the menu
/// writes none of them. A state it has not heard of is shown as given.
public struct PanelFreshness: Decodable, Equatable {
    public let state: String
    public let text: String
    public var detail: String? = nil
    /// What a person does, in words, when no command fits that supervisor.
    public var action: String? = nil
    /// What a person would run, only where the CLI says it is true for that
    /// supervisor. Shown, never run.
    public var command: String? = nil

    public init(state: String, text: String, detail: String? = nil, action: String? = nil, command: String? = nil) {
        self.state = state
        self.text = text
        self.detail = detail
        self.action = action
        self.command = command
    }
}

/// The one line about how many supervisors run the installed build.
public struct PanelFreshnessSummary: Decodable, Equatable {
    public let text: String
    public init(text: String) { self.text = text }
}

public struct PanelList: Decodable, Equatable {
    public var accountQuotas: AccountQuotaList? = nil
    let schemaVersion: Int
    public let registry: String
    public let complete: Bool
    public let allClear: Bool
    public let needsYou: Int
    public let unread: Int
    public let omitted: Int
    public let loops: [PanelLoop]
    /// Changes that can be made to every loop at once (`client review|apply
    /// --all`). Absent from a CLI that cannot.
    public var canAll: [String]? = nil
    /// The installed CLI's build, shortened by the CLI; absent when it has no
    /// version or from a CLI that predates it.
    public var installedBuild: String? = nil
    /// Events to tell the person about, the same list the panel page reads.
    /// Absent from a CLI that predates it.
    public var notices: [PanelNotice]? = nil
    /// The notification settings the menu, the panel and push share, and
    /// whether the menu is notifying. Absent from a CLI that predates it.
    public var notify: PanelNotify? = nil
    /// "Build X installed … · N supervisors on it · M older"; absent from a
    /// CLI that predates it.
    public var buildFreshness: PanelFreshnessSummary? = nil
    public var inactive: Int? = nil
}

/// What `keepgoing panel status --json` says about the panel's background
/// server. It never carries an address, a code or a cookie.
public struct PanelStatus: Decodable, Equatable {
    let schemaVersion: Int
    public let running: Bool
    public let idleRemainingSeconds: Int?
    /// Legacy loop-control flag: true does not imply account/route management.
    public var changesAllowed: Bool? = nil
    /// CLI wire values: read_only, controls, all. Missing/unknown stays unknown.
    public var mode: String? = nil
    /// Only explicit full mode can be reused for management without an upgrade.
    public var runningInFullMode: Bool { running && mode == "all" }
    public var requiresManagementRestart: Bool { running && !runningInFullMode }
    /// The page footer's statement about allowing changes. Absent from a CLI
    /// that predates it, and then the menu does not offer to allow them.
    public var exposure: String? = nil
}

/// When the app reads without being asked. One list read a minute keeps the
/// count beside the icon current; after three failures in a row it slows to
/// one every five minutes; in Low Power Mode it does not read on a timer at
/// all, and the menu still reads when it is opened.
public enum ReadSchedule {
    public static let normal: TimeInterval = 60
    public static let afterFailures: TimeInterval = 300
    public static let failuresBeforeSlowing = 3
    public static func interval(consecutiveFailures: Int, lowPower: Bool) -> TimeInterval? {
        if lowPower { return nil }
        return consecutiveFailures >= failuresBeforeSlowing ? afterFailures : normal
    }
}

extension Contract {
    public static func panelStatus(_ data: Data, exit: Int32) throws -> PanelStatus {
        guard exit == 0, data.count <= 4096 else { throw ClientFailure.unavailable }
        guard let result = try? JSONDecoder().decode(PanelStatus.self, from: data) else { throw ClientFailure.invalidResponse }
        guard result.schemaVersion == 1 else { throw ClientFailure.unsupported }
        guard (result.idleRemainingSeconds ?? 0) >= 0 else { throw ClientFailure.invalidResponse }
        if let exposure = result.exposure, panelAuthority(exposure) == nil { throw ClientFailure.invalidResponse }
        return result
    }

    static func validKey(_ key: String) -> Bool {
        key.range(of: "^l[0-9a-f]{12}$", options: .regularExpression) != nil
    }

    public static func panelList(_ data: Data, exit: Int32) throws -> PanelList {
        guard data.count <= 1_048_576 else { throw ClientFailure.outputLimit }
        let result: PanelList
        do {
            try QuotaJSON.validateProvenanceKeys(data)
            result = try JSONDecoder().decode(PanelList.self, from: data)
        }
        catch { throw ClientFailure.invalidResponse }
        guard result.schemaVersion == 1 else { throw ClientFailure.unsupported }
        // An unreadable registry exits 1 and still says so in a valid reply.
        guard (result.registry == "available" && exit == 0) || (result.registry == "unavailable" && exit == 1),
              result.loops.count <= 128, result.needsYou >= 0, result.unread >= 0, result.unread <= result.needsYou, result.omitted >= 0,
              Set(result.loops.map(\.key)).count == result.loops.count,
              // Checked on what will be shown: a name of control characters only is empty.
              (result.accountQuotas == nil || validQuotas(result.accountQuotas!)),
              result.loops.allSatisfy({ validKey($0.key) && !text($0.name).isEmpty && !text($0.phrase).isEmpty }) else {
            throw ClientFailure.invalidResponse
        }
        return result
    }
}

/// Every command the menu can run, and what a row does when chosen. `nil`
/// on a row means it cannot be chosen. `MenuBuilder.arguments` is the
/// allow-list: nothing else is ever run.
public enum MenuCommand: Equatable {
    /// Runs `keepgoing panel list --json`. Never on a row.
    case readList
    /// Runs `keepgoing panel status --json`. Never on a row.
    case readStatus
    /// Runs `keepgoing panel`: the page opens on what needs a person.
    case openPanel
    /// Opens this account in the panel; no sign-in or provider read.
    case openAccount(id: String)
    case connectAccount(id: String)
    case openAccountWithChanges(id: String)
    /// Runs `keepgoing panel --loop <key>`.
    case openLoop(key: String)
    /// Runs `keepgoing panel --allow-changes`, after a confirmation.
    case openPanelWithChanges
    /// Runs `keepgoing panel stop`. Supervision is not affected.
    case stopPanel
    /// Turns one kind of notification on or off. Runs no CLI command.
    case toggleNoticeKind(String)
    /// Runs `keepgoing panel notify set --kind K --on|--off`: writes the shared
    /// settings. Only a known kind.
    case notifySet(kind: String, on: Bool)
    /// Runs `keepgoing panel notify heartbeat`: records that the menu runs.
    case notifyHeartbeat(notifying: Bool, pid: Int32)
    /// Relaunches the menu from the installed build. Runs no CLI command.
    case relaunchMenu
    /// Turns "Update the menu automatically" on or off. Runs no CLI command.
    case toggleAutoUpdate
    /// Runs `keepgoing client review <action> --loop <key> --json`, which
    /// reads and changes nothing; its review is then shown for confirmation.
    case review(LoopAction, key: String)
    /// Runs `keepgoing client apply <action> --loop <key> --expect <digest>
    /// [--lanes] [--immediately] --json`, only from a confirmed review.
    /// `--drop-queued` deletes messages queued in lanes that have not
    /// started; it is passed only with `--lanes`, and only for the stop
    /// option that says so.
    case apply(LoopAction, key: String, expect: String, lanes: Bool, immediately: Bool, dropQueued: Bool = false)
    /// Runs `keepgoing client review pause|stop --all --json`, which reads
    /// and changes nothing; its review is then shown for confirmation.
    case reviewAll(LoopAction)
    /// Runs `keepgoing client apply pause|stop --all --expect KEY=VALUE…
    /// [--lanes] --json`, only from a confirmed review of every loop.
    /// `--lanes` is for a stop, when its option says so; whether each loop
    /// stops at once is what that loop's own review said.
    case applyAll(LoopAction, expects: [LoopExpect], lanes: Bool = false)
    case quit
}

/// Semantic tint for a row's symbol. Shape and text still carry the meaning
/// without colour; `.needsYou` is reserved for states that wait on a person.
public enum Tone: Equatable {
    case supervising
    case needsYou
    case limit
    case failing
    case quiet
}

public struct MenuRow: Equatable {
    public enum Kind: Equatable { case action, loop, status, separator }
    public let kind: Kind
    /// The main text. For a loop this is its name.
    public let title: String
    /// Secondary text on the same row: a loop's state in the panel's words.
    public let detail: String
    /// An SF Symbol name, or empty for none.
    public let symbol: String
    /// What a screen reader says for the row: what it is.
    public let accessibilityLabel: String
    /// What choosing it does, offered as help so it is not read on every row.
    public let accessibilityHelp: String
    /// The full text when the title was shortened; empty otherwise.
    public let toolTip: String
    public let command: MenuCommand?
    /// A loop's choices, shown as its submenu.
    public var submenu: [MenuRow] = []
    /// Semantic tint for `symbol`; `.quiet` stays a system-coloured template.
    public var tone: Tone = .quiet
    /// Stable presentation identity for non-command submenus whose position can
    /// change. Dynamic titles/countdowns are deliberately not their identity.
    public var semanticID: String? = nil
    public var isEnabled: Bool { command != nil || !submenu.isEmpty }
}

public struct MenuModel: Equatable {
    public let rows: [MenuRow]
    /// Beside the menu bar icon: the count of loops that need a person; "?"
    /// when keepgoing could not be read or not every loop was; and empty only
    /// when a complete read found that nothing needs a person.
    public let badge: String
    public let iconAccessibilityLabel: String
}

/// What the app knows when it draws the menu.
public enum MenuReading: Equatable {
    case notRead
    case reading
    case read(PanelList, at: Date)
    /// The CLI answered in a version this menu does not understand.
    case unsupported
    /// The CLI could not be run, timed out or answered something unreadable.
    case unavailable
}

/// What the menu shows of its notifications.
public struct NoticeView: Equatable {
    public let recent: [RecentNotice]
    public let on: [String: Bool]
    public init(recent: [RecentNotice], on: [String: Bool]) { self.recent = recent; self.on = on }
}

/// What the menu says about its own update.
public struct UpdateOffer: Equatable {
    /// The installed build's name when it differs from the one running.
    public let availableLabel: String?
    public let automatic: Bool
    /// Why the last relaunch did not happen, when it did not.
    public let failure: String?
    /// Set when the installed build ships no menu app: said in the menu
    /// rather than nothing.
    public let noMenuLabel: String?
    public init(availableLabel: String?, automatic: Bool, failure: String? = nil, noMenuLabel: String? = nil) {
        self.availableLabel = availableLabel
        self.automatic = automatic
        self.failure = failure
        self.noMenuLabel = noMenuLabel
    }
}

public enum MenuBuilder {
    // Long enough for a real loop name, short enough that the state column
    // stays close to the short names beside it.
    static let maxName = 32
    static let maxPhrase = 40
    /// The most loops listed. They arrive with the ones that want attention
    /// first; the rest are one choice away in the panel.
    static let maxLoops = 25

    /// One symbol per state, told apart by shape and never by colour. The
    /// needs-you symbol is the only filled one.
    static let symbols: [String: String] = [
        "waiting_for_you": "exclamationmark.circle.fill", "unknown": "questionmark.circle", "checks_failing": "exclamationmark.triangle",
        "not_checked_recently": "questionmark.circle", "waiting_on_limit": "hourglass", "supervising": "circle.inset.filled",
        "outside_hours": "moon", "paused": "pause.circle", "off": "circle", "stopped": "stop.circle", "finished": "checkmark.circle",
    ]

    /// Semantic tint per state, matching every key in `symbols`. Only
    /// `waiting_for_you` maps to `.needsYou`; routine quiet states stay in the
    /// system colour.
    public static let tones: [String: Tone] = [
        "waiting_for_you": .needsYou, "unknown": .quiet, "checks_failing": .failing,
        "not_checked_recently": .quiet, "waiting_on_limit": .limit, "supervising": .supervising,
        "outside_hours": .quiet, "paused": .quiet, "off": .quiet, "stopped": .quiet, "finished": .quiet,
    ]

    /// Shortened at the end, never in the middle: a name's beginning is what
    /// the person reads and what the panel shows, so a long one loses its tail.
    static func shortened(_ text: String, to limit: Int) -> String {
        let characters = Array(text)
        guard characters.count > limit, limit > 1 else { return text }
        return String(characters.prefix(limit - 1)).trimmingCharacters(in: .whitespaces) + "…"
    }

    static func age(_ seconds: TimeInterval) -> String {
        let s = max(0, Int(seconds))
        if s < 60 { return "Read just now" }
        if s < 3600 { return "Read \(s / 60) min ago" }
        if s < 86_400 { return "Read \(s / 3600) h ago" }
        return "Read more than a day ago"
    }

    /// "1 loop needs you", "3 loops need you", "2 loops can't be read", or
    /// both; nil when nothing needs a person. "Can't be read" is the panel's
    /// phrase for such a loop.
    static func needsLine(held: Int, unread: Int) -> String? {
        var parts: [String] = []
        if held > 0 { parts.append(held == 1 ? "1 loop needs you" : "\(held) loops need you") }
        if unread > 0 {
            parts.append(parts.isEmpty ? (unread == 1 ? "1 loop can't be read" : "\(unread) loops can't be read") : "\(unread) can't be read")
        }
        return parts.isEmpty ? nil : parts.joined(separator: ", ")
    }

    static func action(_ title: String, _ command: MenuCommand, symbol: String, help: String, detail: String = "", tone: Tone = .quiet) -> MenuRow {
        MenuRow(kind: .action, title: title, detail: detail, symbol: symbol, accessibilityLabel: detail.isEmpty ? title : title + ", " + detail,
                accessibilityHelp: help, toolTip: "", command: command, tone: tone)
    }
    static let separator = MenuRow(kind: .separator, title: "", detail: "", symbol: "", accessibilityLabel: "", accessibilityHelp: "", toolTip: "", command: nil)
    static func status(_ text: String) -> MenuRow {
        MenuRow(kind: .status, title: text, detail: "", symbol: "", accessibilityLabel: text, accessibilityHelp: "", toolTip: "", command: nil)
    }

    /// The Recent section (the last ten events, pushed or not, with their age)
    /// and the per-kind settings.
    static func noticeRows(_ view: NoticeView, now: Date) -> [MenuRow] {
        var rows: [MenuRow] = []
        if !view.recent.isEmpty {
            // Recent is one item with a submenu: the last ten, pushed or not, with their age.
            let items = view.recent.prefix(NoticeTracker.maxRecent).map { r in status(r.text + " · " + age(now.timeIntervalSince(r.at)).replacingOccurrences(of: "Read ", with: "")) }
            rows.append(MenuRow(kind: .action, title: "Recent", detail: "", symbol: "clock", accessibilityLabel: "Recent", accessibilityHelp: "Shows the last events.", toolTip: "", command: nil, submenu: items))
        }
        let toggles = NoticeKinds.all.map { entry -> MenuRow in
            let on = view.on[entry.kind] ?? false
            return action(entry.label, .toggleNoticeKind(entry.kind), symbol: on ? "checkmark.circle" : "circle",
                          help: "Turns the notification for this on or off. It is still listed under Recent.", detail: on ? "on" : "off")
        }
        let parent = MenuRow(kind: .action, title: "Notify me when…", detail: "", symbol: "bell", accessibilityLabel: "Notify me when",
                             accessibilityHelp: "Chooses which events are pushed as notifications.", toolTip: "", command: nil, submenu: toggles)
        rows.append(parent)
        return rows
    }

    /// The menu's own update: the offer when the installed build differs (and
    /// why the last attempt failed, if it did), then the setting.
    static func updateRows(_ update: UpdateOffer) -> [MenuRow] {
        var rows: [MenuRow] = []
        if let label = update.availableLabel.map(Contract.text) {
            rows.append(action("Update available: relaunch to use \(label)", .relaunchMenu, symbol: "arrow.down.circle",
                               help: "Starts the installed build of this menu and quits this one. Your loops and Open at Login are not affected."))
            if let why = update.failure.map(Contract.text), !why.isEmpty { rows.append(status("The last automatic relaunch did not happen: " + why + ".")) }
        }
        if let label = update.noMenuLabel.map(Contract.text), !label.isEmpty, update.availableLabel == nil {
            rows.append(status("The installed build (" + label + ") has no menu app, so this menu cannot update to it."))
        }
        rows.append(action("Update the menu automatically", .toggleAutoUpdate, symbol: update.automatic ? "checkmark.circle" : "circle",
                           help: "When on, the menu relaunches itself from the installed build while it is closed and no change is being made. Turning it on does not relaunch under an open menu; it happens once the menu is closed.", detail: update.automatic ? "on" : "off"))
        return rows
    }

    /// "stops by itself in 23 min", from the seconds the panel reports.
    static func stopsIn(_ seconds: Int?) -> String {
        guard let seconds else { return "running" }
        if seconds < 60 { return "stops by itself in under a minute" }
        if seconds < 5400 { return "stops by itself in \(seconds / 60) min" }
        return "stops by itself in \(seconds / 3600) h"
    }

    /// `notice` replaces the status line once, for example after the panel
    /// could not be opened. While `refreshing`, the rows of the last read stay
    /// in place and the status line says a read is under way. `panel` is the
    /// panel server's status when it was read; the row that stops it is there
    /// only while it is running.
    public static func build(_ reading: MenuReading, now: Date, notice: String? = nil, refreshing: Bool = false, panel: PanelStatus? = nil, update: UpdateOffer? = nil, notices: NoticeView? = nil, buildStamp: MenuBuildStamp? = nil) -> MenuModel {
        // The first row is always there and always tries, whatever was read.
        var rows = [action("Open looptender Panel", .openPanel, symbol: "macwindow", help: "Opens the looptender panel in your browser, where you can pause, stop and resume loops, each after a review.")]
        if let buildStamp { rows.append(contentsOf: identityRows(buildStamp, reading: reading, update: update)) }
        var badge = ""
        var iconLabel = "looptender"
        var line: String
        switch reading {
        case .notRead:
            line = "Not read yet"
        case .reading:
            line = "Reading…"
        case .unsupported:
            line = "The menu and looptender are different versions. Use the panel."
            badge = "?"
            iconLabel = "looptender, can't be read"
        case .unavailable:
            line = "Can't reach looptender. Run keepgoing doctor."
            badge = "?"
            iconLabel = "looptender, can't be read"
        case .read(let list, let at):
            // The CLI's count, less the loops that still carry a hold but are archived
            // or replaced: a person cannot act on those, so they never count.
            let stale = list.loops.filter { $0.needsYou && group($0) == .other }.count
            let held = max(0, list.needsYou - list.unread - stale)
            if let needs = needsLine(held: held, unread: list.unread) {
                // The symbol says which it is: a person is needed, or only
                // that something could not be read.
                rows.append(action(needs, .openPanel, symbol: held > 0 ? "exclamationmark.circle.fill" : "questionmark.circle", help: "Opens the panel on what needs you.", tone: held > 0 ? .needsYou : .quiet))
                let count = max(0, list.needsYou - stale)
                badge = count > 99 ? "99+" : String(count)
                iconLabel = "looptender, " + needs
            } else if !list.allClear && !(stale > 0 && list.needsYou == stale && list.complete && list.omitted == 0 && list.registry == "available") {
                // The icon is plain only when the CLI says every loop was
                // read and none needs a person. An unreadable registry or a
                // read that was cut off is not "nothing needs you".
                badge = "?"
                iconLabel = list.registry == "available" ? "looptender, not every loop was read" : "looptender, the list of loops can't be read"
            }
            let installed = list.installedBuild.map(Contract.text)
            let ordered = loopsByActivity(list.loops, now: now)
            let groups: [(String, [PanelLoop])] = [("Needs you", ordered.filter { group($0) == .needsYou }), ("Working", ordered.filter { group($0) == .working })]
            let others = ordered.filter { group($0) == .other }
            let listedLoops = groups.flatMap { $0.1.prefix(maxLoops) } + others.prefix(maxLoops * 2)
            let names = disambiguatedTitles(listedLoops)
            if !list.loops.isEmpty { rows.append(separator) }
            for (title, loops) in groups where !loops.isEmpty {
                rows.append(status(title))
                for loop in loops.prefix(maxLoops) { rows.append(loopRow(loop, title: names[loop.key], installed: installed, now: now)) }
            }
            if !others.isEmpty {
                rows.append(MenuRow(kind: .action, title: "Other loops (\(others.count))", detail: "", symbol: "tray.full", accessibilityLabel: "Other loops, \(others.count)",
                                    accessibilityHelp: "Shows paused, off, stopped, finished and archived loops.", toolTip: "", command: nil,
                                    submenu: others.prefix(maxLoops * 2).map { loopRow($0, title: names[$0.key], installed: installed, now: now) }))
            }
            // Accounts: only those at or near a limit on top; the rest in one submenu.
            if let quota = list.accountQuotas {
                let all = quotaRows(quota, now: now)
                let matter = accountsThatMatter(quota, now: now)
                if !matter.isEmpty || !all.isEmpty {
                    rows.append(separator)
                    rows.append(contentsOf: matter)
                    rows.append(MenuRow(kind: .action, title: "Accounts", detail: "", symbol: "person.2", accessibilityLabel: "Accounts",
                                        accessibilityHelp: "Shows saved quota for the selected accounts.", toolTip: "", command: nil, submenu: all))
                }
            }
            // The build, only when something is not on the installed one.
            let behind = notOnInstalledCount(list.loops)
            if behind > 0 { rows.append(status(behind == 1 ? "1 supervisor not on the installed build" : "\(behind) supervisors not on the installed build")) }
            if list.canAll?.contains("pause") == true {
                rows.append(separator)
                rows.append(action("Pause All Loops…", .reviewAll(.pause), symbol: "pause.circle",
                                   help: "Shows which loops pausing every loop changes, then asks you to confirm."))
            }
            if list.canAll?.contains("stop") == true {
                rows.append(action("Stop All Loops…", .reviewAll(.stop), symbol: "stop.circle",
                                   help: "Shows which loops stopping every loop changes, then asks you to confirm."))
            }
            let total = list.loops.count + list.omitted
            // Whenever some loops are not listed here, whether this menu cut
            // them or the CLI did.
            if total > listedLoops.count {
                rows.append(action("All \(total) Loops in the Panel", .openPanel, symbol: "ellipsis.circle", help: "Opens the panel, which lists every loop."))
            }
            var facts: [String] = []
            if list.registry != "available" {
                facts.append("The list of loops could not be read.")
            } else if list.loops.isEmpty {
                facts.append("No loops are registered.")
            } else if !list.complete {
                facts.append("Not every loop was read.")
            }
            // Alone, the age reads as a label; after a sentence it is one too.
            let read = age(now.timeIntervalSince(at))
            line = facts.isEmpty ? read : (facts + [read + "."]).joined(separator: " ")
            // The installed build, once, at the foot. Nothing when the CLI did not say.
            if let installed = list.installedBuild.map(Contract.text), !installed.isEmpty { line += " · looptender " + installed }
        }
        rows.append(separator)
        // Management is distinct from the ordinary loop-controls panel.
        // Only offer the confirmed launcher with the CLI's complete disclosure.
        if let exposure = panel?.exposure, Contract.panelAuthority(exposure) != nil {
            rows.append(action("Open Management Panel…", .openPanelWithChanges, symbol: "slider.horizontal.3", help: "Review account connections, usage settings and preferred routes. Opening does not sign in or run a model probe."))
        }
        if let panel, panel.running {
            // Named for what it stops, the page, so it is not taken for
            // stopping a loop; what happens to the loops is in the help.
            rows.append(action("Stop the Panel Page", .stopPanel, symbol: "xmark.circle", help: "Stops the panel's background process. Your loops keep running.", detail: stopsIn(panel.idleRemainingSeconds)))
        }
        if let notices { rows.append(contentsOf: noticeRows(notices, now: now)) }
        if let update { rows.append(contentsOf: updateRows(update)) }
        rows.append(action("Quit looptender Menu", .quit, symbol: "power", help: "Quits this menu. Supervision keeps running."))
        rows.append(separator)
        rows.append(status(notice ?? (refreshing ? "Reading…" : line)))
        return MenuModel(rows: rows, badge: badge, iconAccessibilityLabel: iconLabel)
    }

    /// The configured provider, and model when known. Empty when the CLI gave none.
    static func targetText(_ loop: PanelLoop) -> String {
        guard let provider = loop.provider.map(Contract.text), !provider.isEmpty else { return "" }
        guard let model = loop.model.map(Contract.text), !model.isEmpty else { return provider }
        return provider + " " + model
    }

    /// What the loop's supervisor last recorded running, and the installed
    /// build when it is a different one. Empty when the CLI gave no build.
    static func buildText(_ loop: PanelLoop, installed: String?) -> String {
        guard let running = loop.recordedBuild.map(Contract.text), !running.isEmpty else { return "" }
        if loop.buildDiffers == true, let installed = installed.map(Contract.text), !installed.isEmpty {
            return "last check ran \(running) · installed \(installed)"
        }
        return running
    }

    /// The mark on a loop's row when its supervisor is not simply on the
    /// installed build: the CLI's own words. Nothing for "current" (the build
    /// text already says the version) and nothing when none was sent.
    static func freshnessMark(_ loop: PanelLoop) -> String {
        guard let f = loop.buildFreshness, f.state != "current" else { return "" }
        return Contract.text(f.text)
    }

    /// The detail and the command for a supervisor that is not on the
    /// installed build, as text in the loop's submenu. Nothing here runs: no
    /// row restarts, resumes or re-enables a supervisor.
    static func freshnessRows(_ loop: PanelLoop) -> [MenuRow] {
        guard let f = loop.buildFreshness, f.state != "current" else { return [] }
        var rows: [MenuRow] = []
        if let detail = f.detail.map(Contract.text), !detail.isEmpty { rows.append(status(detail)) }
        if let action = f.action.map(Contract.text), !action.isEmpty { rows.append(status(action)) }
        if let command = f.command.map(Contract.text), !command.isEmpty { rows.append(status("Run: " + command)) }
        return rows.isEmpty ? [] : [separator] + rows
    }

    /// A loop's submenu: open it in the panel and, when the CLI says which
    /// changes fit its flags, each of those, Stop last. With nothing to
    /// change it has only the one choice, and the row opens the loop itself.
    static func loopChoices(_ loop: PanelLoop) -> [MenuRow] {
        let rows = [action("Open in Panel", .openLoop(key: loop.key), symbol: "macwindow", help: "Opens this loop in the panel.")]
        let can = Set(loop.can ?? [])
        let obs = loop.observed == true
        var changes: [MenuRow] = []
        if can.contains("pause") {
            changes.append(action(obs ? "Pause checking…" : "Pause…", .review(.pause, key: loop.key), symbol: "pause.circle",
                                  help: obs ? "Shows what pausing checking does, then asks you to confirm." : "Shows what pausing does, then asks you to confirm."))
        }
        if can.contains("resume") {
            changes.append(action(obs ? "Resume checking…" : "Resume…", .review(.resume, key: loop.key), symbol: "play.circle",
                                  help: obs ? "Shows what resuming checking clears, then asks you to confirm." : "Shows what resuming clears, then asks you to confirm."))
        }
        if !obs && can.contains("stop") {
            changes.append(action("Stop…", .review(.stop, key: loop.key), symbol: "stop.circle", help: "Shows what stopping does, then asks you to confirm."))
        }
        if !isArchived(loop) && can.contains("archive") {
            changes.append(action("Archive…", .review(.archive, key: loop.key), symbol: "archivebox", help: "Shows what archiving does without deleting files, then asks you to confirm."))
        }
        if isArchived(loop) && can.contains("unarchive") {
            changes.append(action("Unarchive…", .review(.unarchive, key: loop.key), symbol: "tray.and.arrow.up", help: "Shows what unarchiving does without resuming, then asks you to confirm."))
        }
        return changes.isEmpty ? rows : rows + [separator] + changes
    }

    /// The arguments each command runs, before the registry and panel
    /// directories are appended: the menu's whole allow-list. A value taken
    /// from a reply is passed back only after it matches its pattern. Quit
    /// runs nothing.
    public static func arguments(_ command: MenuCommand) -> [String]? {
        switch command {
        case .readList: return ["panel", "list", "--json"]
        case .readStatus: return ["panel", "status", "--json"]
        case .openPanel: return ["panel"]
        case .openAccountWithChanges(let id): return id.count == 32 && id.allSatisfy { "0123456789abcdef".contains($0) } ? ["panel", "--allow-changes", "--account", id] : nil
        case .connectAccount(let id), .openAccount(let id): return id.count == 32 && id.allSatisfy { "0123456789abcdef".contains($0) } ? ["panel", "--account", id] : nil
        case .openLoop(let key): return Contract.validKey(key) ? ["panel", "--loop", key] : nil
        case .openPanelWithChanges: return ["panel", "--allow-changes"]
        case .stopPanel: return ["panel", "stop"]
        case .review(let action, let key): return Contract.validKey(key) ? ["client", "review", action.rawValue, "--loop", key, "--json"] : nil
        case .apply(let action, let key, let expect, let lanes, let immediately, let dropQueued):
            guard Contract.validKey(key), Contract.validExpect(expect), action == .stop || (!lanes && !immediately && !dropQueued), !dropQueued || lanes else { return nil }
            return ["client", "apply", action.rawValue, "--loop", key, "--expect", expect] + (lanes ? ["--lanes"] : []) + (immediately ? ["--immediately"] : [])
                + (dropQueued ? ["--drop-queued"] : []) + ["--json"]
        case .reviewAll(let action): return (action == .pause || action == .stop) ? ["client", "review", action.rawValue, "--all", "--json"] : nil
        case .applyAll(let action, let expects, let lanes):
            let keys = expects.map(\.key)
            guard (action == .pause || action == .stop), !lanes || action == .stop, (1...128).contains(expects.count), Set(keys).count == keys.count,
                  expects.allSatisfy({ Contract.validKey($0.key) && Contract.validExpect($0.expect) }) else { return nil }
            var arguments: [String] = ["client", "apply", action.rawValue, "--all"]
            for e in expects { arguments += ["--expect", e.key + "=" + e.expect] }
            if lanes { arguments.append("--lanes") }
            return arguments + ["--json"]
        case .notifySet(let kind, let on):
            return NoticeKinds.all.contains { $0.kind == kind } ? ["panel", "notify", "set", "--kind", kind, on ? "--on" : "--off"] : nil
        case .notifyHeartbeat(let notifying, let pid): return pid > 0 ? ["panel", "notify", "heartbeat", "--notifying=" + (notifying ? "true" : "false"), "--pid=" + String(pid)] : nil
        case .quit, .relaunchMenu, .toggleAutoUpdate, .toggleNoticeKind: return nil
        }
    }

    /// Structural changes wait while a tracked menu row is highlighted.
    /// Keep this separate from in-place same-shape text refreshes.
    public static func deferStructuralUpdate(menuOpen: Bool, hasHighlightedItem: Bool) -> Bool {
        menuOpen && hasHighlightedItem
    }

    public enum UpdateMode: Equatable { case inPlace, rebuild, deferred }

    /// A structural change must not replace the highlighted interaction target.
    /// Countdown/text changes for the same semantic rows can still update in place.
    public static func updateMode(shown: [MenuRow], next: [MenuRow], itemCount: Int, menuOpen: Bool, hasHighlight: Bool) -> UpdateMode {
        if itemCount == next.count && sameShape(shown, next) { return .inPlace }
        return deferStructuralUpdate(menuOpen: menuOpen, hasHighlightedItem: hasHighlight) ? .deferred : .rebuild
    }

    /// Two models have the same shape when their rows are the same kinds
    /// running the same commands and retaining semantic identities, with submenus
    /// of the same shape, so an open
    /// menu can be updated in place.
    public static func sameShape(_ a: [MenuRow], _ b: [MenuRow]) -> Bool {
        a.count == b.count && zip(a, b).allSatisfy { $0.kind == $1.kind && $0.command == $1.command && $0.semanticID == $1.semanticID && sameShape($0.submenu, $1.submenu) }
    }
}
