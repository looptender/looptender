import Foundation

/// Synthetic native menu execution; no CLI, provider, GUI, or runtime files.
@main struct QuotaClockSmoke {
    static func windows(_ row: MenuRow) -> [MenuRow] { row.submenu.filter { $0.semanticID?.hasPrefix("quota-window:") == true } }
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
        precondition(windows(row)[0].submenu.contains { $0.accessibilityLabel.contains("Exhausted in the saved reading") })
        precondition(windows(row)[0].submenu.contains { $0.accessibilityLabel.contains(reset) && $0.accessibilityLabel.contains("local time") })
        let later = now.addingTimeInterval(3600)
        precondition(MenuBuilder.quotaEnded(live, now: later))
        precondition(MenuBuilder.quotaReset(live, now: later).contains("refresh to confirm"))
        let ended = MenuBuilder.quotaAccountRow(account([live]), now: later)
        precondition(ended.detail == "Reading out of date · reset passed")
        precondition(windows(ended)[0].accessibilityLabel.contains("Last reading: 100% used"))
        precondition(!ended.detail.contains("100.0% left"))
        let mixed = MenuBuilder.quotaAccountRow(a, now: later)
        precondition(windows(mixed)[0].title == "Weekly" && mixed.detail.contains("90.0% left"))
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
        precondition(menu[2].detail.contains("reset passed"))
        precondition(menu[3].detail == "Quota unavailable")
        // Supported individual limits can report remaining without usedPercent.
        let remainingOnly = AccountQuotaRow(name: "Individual limit", text: "87.5% remaining · provider units", usedPercent: nil, remainingPercent: 87.5, resetsAt: "2030-03-04T14:00:00Z", ended: nil)
        let mixedRemaining = MenuBuilder.quotaAccountRow(account([live, remainingOnly]), now: later)
        precondition(mixedRemaining.detail.contains("87.5% left") && !mixedRemaining.detail.contains("reset passed"))
        precondition(windows(mixedRemaining)[0].title == "Individual limit")
        let remainingOrder = MenuBuilder.quotaRows(AccountQuotaList(state: "available", complete: true, accounts: [account([live]), account([remainingOnly])], omitted: 0), now: later)
        precondition(remainingOrder[1].detail.contains("87.5% left") && remainingOrder[2].detail.contains("reset passed"))
        precondition(remainingOnly.usedPercent == nil)
        // Equal-shaped windows swap as the first reset passes. A tracked menu
        // must defer that structural change, while ordinary countdowns update.
        let first = AccountQuotaRow(name: "5-hour", text: "40% used · 60% remaining", usedPercent: 40, remainingPercent: 60, resetsAt: reset, ended: nil)
        let second = AccountQuotaRow(name: "7-day", text: "80% used · 20% remaining", usedPercent: 80, remainingPercent: 20, resetsAt: "2030-03-04T14:00:00Z", ended: nil)
        let beforeSwap = MenuBuilder.quotaAccountRow(account([first, second]), now: now)
        let ticking = MenuBuilder.quotaAccountRow(account([first, second]), now: now.addingTimeInterval(60))
        let afterSwap = MenuBuilder.quotaAccountRow(account([first, second]), now: later)
        precondition(MenuBuilder.sameShape([beforeSwap], [ticking]))
        precondition(!MenuBuilder.sameShape([beforeSwap], [afterSwap]))
        precondition(windows(beforeSwap)[0].semanticID == windows(afterSwap)[1].semanticID)
        precondition(windows(beforeSwap)[1].semanticID == windows(afterSwap)[0].semanticID)
        precondition(MenuBuilder.updateMode(shown: [beforeSwap], next: [afterSwap], itemCount: 1, menuOpen: true, hasHighlight: true) == .deferred)
        precondition(MenuBuilder.updateMode(shown: [beforeSwap], next: [ticking], itemCount: 1, menuOpen: true, hasHighlight: true) == .inPlace)
        precondition(MenuBuilder.updateMode(shown: [beforeSwap], next: [afterSwap], itemCount: 1, menuOpen: false, hasHighlight: false) == .rebuild)
        precondition(beforeSwap.submenu.first?.command == afterSwap.submenu.first?.command)
        if CommandLine.arguments.contains("--describe") {
            func show(_ row: MenuRow, _ depth: Int = 0) {
                print(String(repeating: "  ", count: depth) + row.title + (row.detail.isEmpty ? "" : " · " + row.detail))
                row.submenu.forEach { show($0, depth + 1) }
            }
            [row, ended, unavailable].forEach { show($0) }
        }
        print("Native quota clocks PASS: remaining/exhausted, exact reset, passed/mixed windows, stale/future time, unknown provider freshness, unavailable navigation, remaining-only activity, tracked reorder and promotion boundary")
    }
}
