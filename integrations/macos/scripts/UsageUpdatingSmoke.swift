import Foundation

/// Fictional scheduler evidence; runs without XCTest and never executes a CLI.
@main struct UsageUpdatingSmoke {
    static func main() throws {
        let base: [String: Any] = ["schemaVersion": 1, "registry": "available", "complete": true, "allClear": true, "needsYou": 0, "unread": 0, "omitted": 0, "loops": []]
        let summary: [String: Any] = ["state": "reported", "text": "Saved selected-context reading", "source": "codex account/rateLimits/read", "observedAt": "2030-03-04T12:00:00Z", "freshness": "stale", "rows": [["name": "5-hour", "text": "40% remaining", "usedPercent": 60, "remainingPercent": 40]], "failedRefreshAt": "2030-03-04T13:00:00Z", "failedRefreshText": "Earlier evidence retains its time."]
        func payload(_ status: [String: Any]?, eligible: Any? = nil) throws -> Data {
            var account: [String: Any] = ["label": "Example updates", "provider": "codex", "summary": summary]
            if let status { account["usageUpdating"] = status }
            var root = base
            var quotas: [String: Any] = ["state": "available", "complete": true, "omitted": 0, "accounts": [account]]
            if let eligible { quotas["workerEligible"] = eligible }
            root["accountQuotas"] = quotas
            return try JSONSerialization.data(withJSONObject: root)
        }
        for state in ["off", "unavailable", "waiting", "paused", "enabled", "backoff"] {
            let list = try Contract.panelList(payload(["state": state, "text": "Fixture \(state)", "nextAttemptAt": "2030-03-04T14:00:00Z"]), exit: 0)
            let model = MenuBuilder.build(.read(list, at: Date()), now: Date())
            guard let row = (model.rows + model.rows.flatMap(\.submenu)).first(where: { $0.title == "Example updates" }),
                  row.submenu.contains(where: { $0.accessibilityLabel == "Fixture \(state)" }),
                  row.submenu.contains(where: { $0.accessibilityLabel.contains("Last successful fetch 2030-03-04T12:00:00Z") }),
                  row.submenu.contains(where: { $0.accessibilityLabel.contains("Refresh failed 2030-03-04T13:00:00Z") }),
                  row.submenu.compactMap(\.command) == [.openPanel],
                  (!["waiting", "paused"].contains(state) || row.detail.contains("updates paused")),
                  (state != "backoff" || row.detail.contains("fetch failed")) else { fatalError("Scheduler evidence hid saved time or introduced authority") }
        }
        for bad: [String: Any] in [["state": "invented", "text": "invalid"], ["state": "enabled", "text": "valid", "nextAttemptAt": "invalid"]] {
            do { _ = try Contract.panelList(payload(bad), exit: 0); fatalError("Invalid scheduler evidence accepted") } catch {}
        }
        guard try Contract.panelList(payload(nil), exit: 0).accountQuotas?.accounts.first?.usageUpdating == nil else { fatalError("Old CLI fallback lost") }
        guard try Contract.panelList(payload(nil, eligible: true), exit: 0).accountQuotas?.workerEligible == true,
              try Contract.panelList(payload(nil), exit: 0).accountQuotas?.workerEligible == nil else { fatalError("Worker eligibility/old CLI fallback lost") }
        do { _ = try Contract.panelList(payload(nil, eligible: "true"), exit: 0); fatalError("Malformed worker eligibility accepted") } catch {}
        print("Native usage updating fixture passed: six states, original successful/failed times, strict optional evidence, no polling authority")
    }
}
