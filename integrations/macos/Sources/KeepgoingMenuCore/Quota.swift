import Foundation

/// Saved selected-context evidence only. Report reads never refresh providers.
public struct AccountQuotaList: Decodable, Equatable {
    public var workerEligible: Bool? = nil
    public let state: String
    public let complete: Bool
    public let accounts: [AccountQuota]
    public let omitted: Int
}
public struct UsageUpdating: Decodable, Equatable {
    public let state: String
    public let text: String
    public let nextAttemptAt: String?
}
public struct AccountQuota: Decodable, Equatable {
    public var usageUpdating: UsageUpdating? = nil
    public let id: String?
    public let label: String
    public let provider: String
    public let summary: AccountQuotaSummary
}
public struct AccountQuotaSummary: Decodable, Equatable {
    public let state: String
    public let text: String
    public let source: String?
    public let observedAt: String?
    public let retrievedAt: String?
    public let freshness: String
    public let rows: [AccountQuotaRow]
    public let partial: Bool?
    public let omitted: Int?
    public let failedRefreshAt: String?
    public let failedRefreshText: String?
}
public struct AccountQuotaRow: Decodable, Equatable {
    public let name: String
    public let text: String
    public let usedPercent: Double?
    public let remainingPercent: Double?
    public let resetsAt: String?
    public let ended: Bool?
}
extension Contract {
    static func quotaText(_ value: String, bytes: Int = 512) -> Bool {
        !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && value.utf8.count <= bytes && value.unicodeScalars.allSatisfy { [0x200C, 0x200D].contains($0.value) || !(CharacterSet.controlCharacters.contains($0) || CharacterSet.newlines.contains($0)) }
    }
    static func validQuotas(_ value: AccountQuotaList) -> Bool {
        guard ["available", "unavailable"].contains(value.state), value.accounts.count <= 16,
              value.omitted >= 0, value.omitted <= 64 else { return false }
        for a in value.accounts {
            let q = a.summary
            if let u = a.usageUpdating {
                guard ["off", "unavailable", "paused", "waiting", "enabled", "backoff"].contains(u.state), quotaText(u.text), u.nextAttemptAt == nil || date(u.nextAttemptAt) != nil else { return false }
            }
            guard a.id == nil || (a.id!.count == 32 && a.id!.allSatisfy { "0123456789abcdef".contains($0) }), quotaText(a.label), ["codex", "claude-code", "antigravity"].contains(a.provider),
                  ["reported", "unavailable"].contains(q.state), quotaText(q.text),
                  ["fresh", "stale", "unavailable", "provider_unknown"].contains(q.freshness), q.rows.count <= 41,
                  (q.omitted ?? 0) >= 0, (q.omitted ?? 0) <= 256,
                  q.observedAt == nil || date(q.observedAt) != nil,
                  q.retrievedAt == nil || date(q.retrievedAt) != nil,
                  q.failedRefreshAt == nil || date(q.failedRefreshAt) != nil,
                  q.failedRefreshText == nil || quotaText(q.failedRefreshText!),
                  q.source == nil || ["codex account/rateLimits/read", "claude:get_usage", "agy:/usage"].contains(q.source!) else { return false }
            if q.state == "reported" {
                let codex = a.provider == "codex" && q.source == "codex account/rateLimits/read" && q.observedAt != nil && q.retrievedAt == nil && ["fresh", "stale"].contains(q.freshness)
                let claude = a.provider == "claude-code" && q.source == "claude:get_usage" && q.retrievedAt != nil && q.observedAt == nil && q.freshness == "provider_unknown"
                let antigravity = a.provider == "antigravity" && q.source == "agy:/usage" && q.retrievedAt != nil && q.observedAt == nil && q.freshness == "provider_unknown"
                guard codex || claude || antigravity else { return false }
            } else if !q.rows.isEmpty || q.observedAt != nil || q.retrievedAt != nil || q.source != nil || q.freshness != "unavailable" { return false }
            for r in q.rows {
                guard quotaText(r.name, bytes: 160), quotaText(r.text),
                      r.resetsAt == nil || date(r.resetsAt) != nil else { return false }
                for p in [r.usedPercent, r.remainingPercent].compactMap({ $0 }) {
                    guard p.isFinite && p >= 0 && p <= 100 else { return false }
                }
                if let used = r.usedPercent, let remaining = r.remainingPercent,
                   abs(used + remaining - 100) > 0.000001 { return false }
            }
        }
        return true
    }
}
extension MenuBuilder {
    static func quotaRows(_ list: AccountQuotaList?, now: Date = Date()) -> [MenuRow] {
        guard let list else {
            return [quotaStatus("Account quota is not reported by this CLI.")]
        }
        guard list.state == "available" else { return [quotaStatus("Account quota evidence unavailable.")] }
        if list.accounts.isEmpty { return [quotaStatus("No selected Claude, Codex or Antigravity accounts.")] }
        var result = [quotaStatus("Account quota · saved selections, not live thread identity")]
        func rank(_ account: AccountQuota) -> Int {
            if account.summary.state != "reported" { return 2 }
            return account.summary.rows.contains { quotaWindow($0) && !quotaEnded($0, now: now) } ? 0 : 1
        }
        let ordered = list.accounts.enumerated().sorted {
            let lhs = rank($0.element), rhs = rank($1.element)
            return lhs == rhs ? $0.offset < $1.offset : lhs < rhs
        }
        for item in ordered.prefix(6) {
            result.append(quotaAccountRow(item.element, now: now))
        }
        let more = list.omitted + max(0, list.accounts.count - 6)
        if more > 0 { result.append(quotaStatus("\(more) more selected accounts in the panel.")) }
        if !list.complete { result.append(quotaStatus("Account selection evidence is incomplete.")) }
        return result
    }
    static func quotaWindow(_ row: AccountQuotaRow) -> Bool {
        row.usedPercent != nil || row.remainingPercent != nil || row.resetsAt != nil
    }
    static func quotaEnded(_ row: AccountQuotaRow, now: Date) -> Bool {
        row.ended == true || Contract.date(row.resetsAt).map { $0 <= now } == true
    }
    static func quotaDuration(_ seconds: TimeInterval) -> String {
        let minutes = max(1, Int(ceil(abs(seconds) / 60)))
        if minutes < 60 { return "\(minutes) min" }
        if minutes < 1440 { return "\(minutes / 60) h" + (minutes % 60 == 0 ? "" : " \(minutes % 60) min") }
        return "\(minutes / 1440) d \((minutes % 1440) / 60) h"
    }
    static func quotaReset(_ row: AccountQuotaRow, now: Date) -> String {
        if quotaEnded(row, now: now) { return "Reset passed · refresh to confirm" }
        guard let at = Contract.date(row.resetsAt) else { return "Reset time unavailable" }
        return "Resets in \(quotaDuration(at.timeIntervalSince(now)))"
    }
    static func quotaAge(_ summary: AccountQuotaSummary, now: Date) -> String {
        let retrieved = summary.retrievedAt != nil
        guard let at = Contract.date(summary.retrievedAt ?? summary.observedAt), at <= now else { return "Reading time unavailable · check device clock" }
        let age = now.timeIntervalSince(at)
        let stamp = age < 60 ? "just now" : "\(quotaDuration(age)) ago"
        if retrieved { return "Retrieved \(stamp) · provider freshness unknown" }
        return "Read \(stamp) · " + (summary.freshness == "stale" || age > 1800 ? "older reading" : "saved snapshot")
    }
    static func quotaAccountRow(_ a: AccountQuota, now: Date = Date()) -> MenuRow {
        let q = a.summary
        let open = MenuRow(kind: .action, title: "Open Account in Panel…", detail: "", symbol: "macwindow", accessibilityLabel: "Open account \(a.label) in panel", accessibilityHelp: "Review quota refresh or automatic usage settings in Accounts with changes allowed. Opening this page does not refresh usage or sign in.", toolTip: "", command: a.id.map { .openAccount(id: $0) } ?? .openPanel)
        var children = [open]
        if q.state == "reported" { children.append(quotaStatus(quotaAge(q, now: now))) }
        else { children.append(quotaStatus("Quota unavailable · \(q.text)")) }
        // Active windows precede expired history, retaining provider order within each group.
        let indexed = Array(q.rows.enumerated())
        let ordered = indexed.filter { !quotaEnded($0.element, now: now) } + indexed.filter { quotaEnded($0.element, now: now) }
        for item in ordered.prefix(8) {
            let r = item.element
            let ended = quotaEnded(r, now: now)
            let value = r.text.replacingOccurrences(of: "Window ended; refresh needed. Last reading: ", with: "")
            let label = "\(r.name): " + (ended ? "Last reading: " : "") + value
            let hasWindow = r.usedPercent != nil || r.remainingPercent != nil || r.resetsAt != nil
            var details = [quotaStatus(label)]
            if hasWindow {
                details.append(quotaStatus(quotaReset(r, now: now)))
            }
            if let reset = r.resetsAt, let at = Contract.date(reset) {
                let local = DateFormatter.localizedString(from: at, dateStyle: .medium, timeStyle: .short)
                details.append(quotaStatus("Reset: \(local) (local time) · \(reset)"))
            }
            if r.usedPercent == 100 { details.append(quotaStatus("Exhausted in the saved reading; new evidence is needed.")) }
            children.append(MenuRow(kind: .action, title: shortened(r.name, to: 40), detail: ended ? "Reset passed · last reading" : r.remainingPercent.map { "\($0)% left" } ?? shortened(r.text, to: 45), symbol: "", accessibilityLabel: label + (hasWindow ? ". " + quotaReset(r, now: now) : ""), accessibilityHelp: "Shows the saved reading and reset detail; never assumes replenishment.", toolTip: label, command: nil, submenu: details, semanticID: "quota-window:\(item.offset):\(r.name)"))
        }
        if q.rows.count > 8 { children.append(quotaStatus("\(q.rows.count - 8) more quota details in the panel.")) }
        if q.partial == true || (q.omitted ?? 0) > 0 { children.append(quotaStatus("Partial quota evidence; missing values are not zero.")) }
        if let u = a.usageUpdating {
            children.append(quotaStatus(u.text))
            if let next = u.nextAttemptAt { children.append(quotaStatus("Next eligible fetch \(next)")) }
        }
        if let at = q.retrievedAt ?? q.observedAt { children.append(quotaStatus("Last successful fetch \(at)")) }
        if let at = q.retrievedAt ?? q.observedAt { children.append(quotaStatus("\(q.retrievedAt == nil ? "Read" : "Retrieved") \(at)")) }
        if q.retrievedAt != nil { children.append(quotaStatus("Provider freshness unknown; usage may be cached or seeded.")) }
        if let source = q.source { children.append(quotaStatus("Source: \(source)")) }
        if let failed = q.failedRefreshAt { children.append(quotaStatus("Refresh failed \(failed). \(q.failedRefreshText ?? "Earlier evidence retains its time.")")) }
        if q.state == "reported" { children.append(quotaStatus(q.text)) }
        children.append(quotaStatus("Refresh usage: open this account in Accounts to review retrieval or Keep usage updated settings."))
        let windows = q.rows.filter { quotaWindow($0) }
        let active = windows.filter { !quotaEnded($0, now: now) }
        // Compare only the supplied percentages. Complementing used here is
        // a ranking key, never a new reported value or replenishment estimate.
        func remainingRank(_ row: AccountQuotaRow) -> Double {
            row.remainingPercent ?? row.usedPercent.map { 100 - $0 } ?? 101
        }
        let limiting = active.min { remainingRank($0) < remainingRank($1) }
        var detail: String
        if q.state != "reported" { detail = "Quota unavailable" }
        else if let limiting {
            let value = limiting.remainingPercent.map { "\($0)% left" } ?? limiting.usedPercent.map { "\($0)% used" } ?? "Quota details recorded"
            detail = value + " · " + quotaReset(limiting, now: now)
        } else { detail = windows.isEmpty ? (q.partial == true ? "Partial quota evidence" : "Quota details recorded") : "Reset passed · refresh needed" }
        if let state = a.usageUpdating?.state {
            if ["waiting", "paused"].contains(state) { detail += " · updates paused" }
            else if state == "backoff" { detail += " · fetch failed" }
            else if state == "unavailable" { detail += " · update settings unavailable" }
        }
        return MenuRow(kind: .action, title: shortened(a.label, to: maxName), detail: detail, symbol: "gauge.with.dots.needle.33percent", accessibilityLabel: "\(a.label), \(a.provider), \(detail). Selected account identity not verified.", accessibilityHelp: "Opens saved quota details; reads no provider.", toolTip: a.label, command: nil, submenu: children, semanticID: "quota-account:\(a.provider):\(a.id ?? a.label)")
    }
    private static func quotaStatus(_ full: String) -> MenuRow {
        MenuRow(kind: .status, title: shortened(full, to: 110), detail: "", symbol: "", accessibilityLabel: full, accessibilityHelp: "", toolTip: full, command: nil)
    }
}
