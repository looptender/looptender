import Foundation

/// Synthetic native menu execution; no CLI, provider, GUI, or runtime files.
@main struct QuotaClockSmoke {
    static func main() {
        let now = Contract.date("2030-03-04T12:00:00Z")!
        let reset = "2030-03-04T13:00:00Z"
        let live = AccountQuotaRow(name: "5-hour", text: "100% used · 0% remaining", usedPercent: 100, remainingPercent: 0, resetsAt: reset, ended: false)
        let missing = AccountQuotaRow(name: "Weekly", text: "10% used · 90% remaining", usedPercent: 10, remainingPercent: 90, resetsAt: nil, ended: nil)
        func account(_ rows: [AccountQuotaRow], source: String = "codex account/rateLimits/read", provider: String = "codex", retrieved: Bool = false, state: String = "reported") -> AccountQuota {
            AccountQuota(id: String(repeating: "a", count: 32), label: "Example account", provider: provider, summary: AccountQuotaSummary(state: state, text: "Saved fictional quota; identity unverified.", source: state == "reported" ? source : nil, observedAt: state == "reported" && !retrieved ? "2030-03-04T12:00:00Z" : nil, retrievedAt: retrieved ? "2030-03-04T12:00:00Z" : nil, freshness: retrieved ? "provider_unknown" : state == "reported" ? "fresh" : "unavailable", rows: rows, partial: false, omitted: 0, failedRefreshAt: nil, failedRefreshText: nil))
        }
        let a = account([live, missing])
        let row = MenuBuilder.quotaAccountRow(a, now: now)
        precondition(row.detail.contains("0.0% left") && row.detail.contains("Resets in 1 h"))
        precondition(row.submenu.first?.command == .openAccount(id: a.id!))
        precondition(row.submenu[2].submenu.contains { $0.accessibilityLabel.contains("Exhausted in the saved reading") })
        precondition(row.submenu[2].submenu.contains { $0.accessibilityLabel.contains(reset) && $0.accessibilityLabel.contains("local time") })
        let later = now.addingTimeInterval(3600)
        precondition(MenuBuilder.quotaEnded(live, now: later))
        precondition(MenuBuilder.quotaReset(live, now: later).contains("refresh to confirm"))
        let ended = MenuBuilder.quotaAccountRow(account([live]), now: later)
        precondition(ended.detail == "Reset passed · refresh needed")
        precondition(ended.submenu[2].accessibilityLabel.contains("Last reading: 100% used"))
        precondition(!ended.detail.contains("100.0% left"))
        let mixed = MenuBuilder.quotaAccountRow(a, now: later)
        precondition(mixed.submenu[2].title == "Weekly" && mixed.detail.contains("90.0% left"))
        precondition(MenuBuilder.quotaReset(missing, now: later) == "Reset time unavailable")
        precondition(MenuBuilder.quotaAge(a.summary, now: now.addingTimeInterval(1801)).contains("older reading"))
        precondition(MenuBuilder.quotaAge(a.summary, now: now.addingTimeInterval(-1)).contains("device clock"))
        let claude = account([missing], source: "claude:get_usage", provider: "claude-code", retrieved: true)
        precondition(MenuBuilder.quotaAge(claude.summary, now: later).contains("provider freshness unknown"))
        let agy = account([missing], source: "agy:/usage", provider: "antigravity", retrieved: true)
        precondition(MenuBuilder.quotaAge(agy.summary, now: later).contains("provider freshness unknown"))
        let unavailable = MenuBuilder.quotaAccountRow(account([], state: "unavailable"), now: now)
        precondition(unavailable.detail == "Quota unavailable" && unavailable.command == nil)
        precondition(unavailable.submenu.compactMap(\.command) == [.openAccount(id: a.id!)])
        precondition(!unavailable.detail.contains("Connect") && !unavailable.detail.contains("0%"))
        let quotas = AccountQuotaList(state: "available", complete: true, accounts: [account([live])], omitted: 0)
        precondition(MenuBuilder.accountsThatMatter(quotas, now: now).count == 1)
        precondition(MenuBuilder.accountsThatMatter(quotas, now: later).isEmpty)
        let empty = account([], state: "unavailable")
        let menu = MenuBuilder.quotaRows(AccountQuotaList(state: "available", complete: true, accounts: [empty, account([live]), account([missing])], omitted: 0), now: later)
        precondition(menu[1].detail.contains("90.0% left"))
        precondition(menu[2].detail.contains("Reset passed"))
        precondition(menu[3].detail == "Quota unavailable")
        if CommandLine.arguments.contains("--describe") {
            func show(_ row: MenuRow, _ depth: Int = 0) {
                print(String(repeating: "  ", count: depth) + row.title + (row.detail.isEmpty ? "" : " · " + row.detail))
                row.submenu.forEach { show($0, depth + 1) }
            }
            [row, ended, unavailable].forEach { show($0) }
        }
        print("Native quota clocks PASS: remaining/exhausted, exact reset, passed/mixed windows, stale/future time, unknown provider freshness, unavailable navigation and promotion boundary")
    }
}
