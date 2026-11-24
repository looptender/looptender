import Foundation

// How the menu groups and words the loops. A row is the loop's display name
// and one short state word; adapter, model, build and times are in the row's
// submenu. Loops are grouped: "Needs you" (what a person can act on now),
// "Working", and one collapsed "Other loops (N)" for paused, off, stopped,
// finished, archived and retired ones. Presentation only: no state is
// invented, and what a control does is unchanged.

public enum LoopGroup: Equatable {
    case needsYou, working, other
}

extension MenuBuilder {
    /// A loop that was replaced by a successor: out of the way, whatever hold it kept.
    static func isRetired(_ loop: PanelLoop) -> Bool { Contract.text(loop.phrase) == "Superseded" }

    static func isArchived(_ loop: PanelLoop) -> Bool { loop.archived != nil || loop.state == "archived" }

    /// The group a loop is listed in. Archived and retired loops are never in
    /// the first two; a loop needs you only when the CLI says a person is
    /// needed and it is neither.
    public static func group(_ loop: PanelLoop) -> LoopGroup {
        if isArchived(loop) || isRetired(loop) { return .other }
        if loop.needsYou { return .needsYou }
        switch loop.state {
        case "paused", "off", "stopped", "finished": return .other
        default: return .working
        }
    }

    /// The name shown: the display name, else the registered name; the CLI's
    /// own short form when it gave one.
    static func displayName(_ loop: PanelLoop) -> String {
        if let n = loop.naming {
            let short = n.shortName.map(Contract.text) ?? ""
            if !short.isEmpty { return short }
            let display = Contract.text(n.displayName)
            if !display.isEmpty { return display }
        }
        // A registered name that is only a thread ID in disguise is never shown.
        let slug = Contract.text(loop.name)
        if slug.hasPrefix("bb-thr-") || slug.hasPrefix("thr_") { return "A loop" }
        return slug
    }

    /// One short word for a state. Known states have a fixed word; a state this
    /// build has not heard of keeps the CLI's phrase, cut at its end.
    static func stateWord(_ loop: PanelLoop) -> String {
        if isArchived(loop) { return "Archived" }
        if isRetired(loop) { return "Replaced" }
        if loop.needsYou {
            // In the Needs you group, say what it waits for, not that it needs you twice.
            switch loop.waiting?.kind {
            case "asked": return "Asked you"
            case "held": return "On hold"
            case "checks": return "Checks stopped"
            case "limit": return "At a limit"
            default: return "Needs you"
            }
        }
        switch loop.state {
        case "supervising": return "Working"
        case "paused": return "Paused"
        case "stopped": return "Stopped"
        case "finished": return "Finished"
        case "off": return "Off"
        case "waiting_on_limit": return "At a limit"
        case "waiting_on_network": return "Offline"
        case "checks_failing": return "Checks failing"
        case "not_checked_recently": return "Not checked"
        case "outside_hours": return "Quiet hours"
        case "waiting_for_you": return "Needs you"
        default: return shortened(Contract.text(loop.phrase), to: 24)
        }
    }

    static func ago(_ date: Date, now: Date) -> String {
        let s = max(0, Int(now.timeIntervalSince(date)))
        if s < 60 { return "just now" }
        if s < 3600 { return "\(s / 60) min ago" }
        if s < 86_400 { return "\(s / 3600) h ago" }
        return "\(s / 86_400) d ago"
    }

    /// What goes in a loop's submenu above its choices: where it runs, the build,
    /// when it was last checked and what it waits for. Fixed words and labels
    /// only: no thread ID, path, note or account identity.
    static func loopFacts(_ loop: PanelLoop, installed: String?, now: Date) -> [MenuRow] {
        var rows: [MenuRow] = []
        let flag = loop.noProgress.map(Contract.text) ?? ""
        let reason = loop.reason.map(Contract.text) ?? ""
        if !reason.isEmpty { rows.append(status(reason)) }
        if !flag.isEmpty { rows.append(status(flag)) }
        let target = targetText(loop)
        if !target.isEmpty { rows.append(status(target)) }
        let build = buildText(loop, installed: installed)
        if !build.isEmpty { rows.append(status(build)) }
        if let f = loop.buildFreshness, f.state != "current" { rows.append(status(Contract.text(f.text))) }
        if let at = Contract.date(loop.checkedAt) { rows.append(status("Checked " + ago(at, now: now))) }
        return rows.isEmpty ? [] : rows + [separator]
    }

    /// A loop's row: display name, one state word, and a submenu.
    static func loopRow(_ loop: PanelLoop, installed: String?, now: Date) -> MenuRow {
        let full = displayName(loop), name = shortened(full, to: maxName)
        let word = stateWord(loop)
        let facts = loopFacts(loop, installed: installed, now: now)
        let choices = loopChoices(loop) + freshnessRows(loop)
        let submenu = facts.isEmpty && choices.count <= 1 ? [] : facts + choices
        let stateKey = loop.needsYou && loop.state == "paused" ? "waiting_for_you" : loop.state
        let tone: Tone = group(loop) == .other ? .quiet : (tones[stateKey] ?? .quiet)
        let symbol = group(loop) == .other && stateKey == "waiting_for_you" ? "pause.circle" : (symbols[stateKey] ?? "questionmark.circle")
        return MenuRow(kind: .loop, title: name, detail: word, symbol: symbol,
                       accessibilityLabel: "\(full), \(word)",
                       accessibilityHelp: submenu.isEmpty ? "Opens this loop in the panel." : "Shows this loop's details and what you can do with it.",
                       toolTip: name == full ? "" : full, command: submenu.isEmpty ? .openLoop(key: loop.key) : nil, submenu: submenu, tone: tone)
    }

    /// Counts the loops a person can act on now, for the line and the icon.
    static func needsYouCount(_ loops: [PanelLoop]) -> Int { loops.filter { group($0) == .needsYou }.count }

    /// Supervisors that are not on the installed build, among loops still in play.
    static func notOnInstalledCount(_ loops: [PanelLoop]) -> Int {
        loops.filter { !isArchived($0) && !isRetired($0) }.filter {
            guard let s = $0.buildFreshness?.state else { return false }
            return s != "current" && s != "updated"
        }.count
    }

    /// The accounts that matter now (at or near a limit), as top-level rows; at most two.
    static func accountsThatMatter(_ rows: [MenuRow]) -> [MenuRow] {
        let matters = rows.filter { row in
            guard row.kind == .action, row.symbol == "gauge.with.dots.needle.33percent", let pct = row.detail.split(separator: "%").first.flatMap({ Double($0) }) else { return false }
            return pct >= 80
        }
        return Array(matters.prefix(2))
    }
}
