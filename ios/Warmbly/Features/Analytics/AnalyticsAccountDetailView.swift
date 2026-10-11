import SwiftUI

/// One mailbox's health, backed by GET analytics/accounts/:id and seeded with
/// the list row so the push renders at once: its score and issues, today's
/// sending, its warmup ramp and standing, where warmup landed, and errors.
struct AnalyticsAccountDetailView: View {
    @Environment(AppEnvironment.self) private var env
    let account: AccountHealthRow

    @State private var detail: AccountHealthRow?
    @State private var errorMessage: String?
    @State private var shown = false

    private var current: AccountHealthRow { detail ?? account }
    private var presenceKey: String { "mailbox:\(account.id)" }

    var body: some View {
        SettingsPage(title: "Mailbox health", spacing: 16) {
            heroCard.reveal(shown, 0)
            if let errorMessage, detail == nil {
                AnalyticsSectionError(text: errorMessage).coverCard()
            }
            if let usage = current.dailyUsage {
                todayCard(usage).reveal(shown, 1)
            }
            if current.warmupStatus?.enabled == true || current.warmupHealth?.state != nil {
                warmupCard.reveal(shown, 2)
            }
            if let placement = current.warmupPlacement, (placement.delivered ?? 0) > 0 {
                placementCard(placement).reveal(shown, 3)
            }
            if let errors = current.errors, !errors.isEmpty {
                errorsCard(errors).reveal(shown, 4)
            }
        }
        .task {
            shown = true
            await load()
        }
        .refreshable { await load() }
        .onChange(of: env.realtime.pulse(for: .emailAccounts)) {
            Task { await load() }
        }
        .presenceResource(presenceKey)
    }

    private func load() async {
        do {
            let fresh: AccountHealthRow = try await env.api.get("analytics/accounts/\(account.id)")
            withAnimation(.snappy) { detail = fresh }
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    // MARK: Hero

    private var heroCard: some View {
        let tone = AnalyticsToneMap.health(current.health?.status)
        let score = current.health?.score ?? 0
        let issues = current.health?.issues ?? []
        return VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 14) {
                MailboxAvatar(email: current.email ?? "", avatarURL: account.avatarURL, brand: account.resolvedBrand, seed: current.id, size: 52)
                VStack(alignment: .leading, spacing: 3) {
                    Text(current.email ?? "Mailbox")
                        .font(.system(size: 17, weight: .semibold))
                        .foregroundStyle(WTheme.ink)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Text(subtitle)
                        .font(.system(size: 13.5))
                        .foregroundStyle(WTheme.inkSecondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 4)
                ResourceViewers(resource: presenceKey)
            }
            Hairline()
            HStack(spacing: 18) {
                SweepRing(fraction: Double(score) / 100, color: tone.color, shown: shown, lineWidth: 9) {
                    CountUpText(value: shown ? Double(score) : 0, format: { "\(Int($0.rounded()))" })
                        .font(.system(size: 28, weight: .semibold, design: .rounded))
                        .monospacedDigit()
                        .foregroundStyle(WTheme.ink)
                        .animation(.easeOut(duration: 1.1).delay(0.15), value: shown)
                }
                .frame(width: 92, height: 92)
                VStack(alignment: .leading, spacing: 6) {
                    StatusPill(text: healthLabel, tone: tone)
                    Text(issues.isEmpty ? "Nothing needs attention" : "\(issues.count) \(issues.count == 1 ? "thing" : "things") to look at")
                        .font(.system(size: 16, weight: .semibold))
                        .foregroundStyle(WTheme.ink)
                    Text("Health score out of 100")
                        .font(.system(size: 13))
                        .foregroundStyle(WTheme.inkTertiary)
                }
                Spacer(minLength: 0)
            }
            if !issues.isEmpty {
                VStack(alignment: .leading, spacing: 10) {
                    ForEach(issues, id: \.self) { issue in
                        HStack(alignment: .top, spacing: 10) {
                            Image(systemName: "exclamationmark.circle.fill")
                                .font(.system(size: 14))
                                .foregroundStyle(WTheme.warning)
                                .padding(.top, 1)
                            Text(issue)
                                .font(.system(size: 14.5))
                                .foregroundStyle(WTheme.ink)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Tone.amber.background, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            }
        }
        .coverCard()
    }

    private var subtitle: String {
        var parts = [account.providerLabel]
        if let synced = current.lastSyncedAt { parts.append("synced \(WFormat.ago(synced)) ago") }
        if current.inCampaign == true { parts.append("in a campaign") }
        return parts.joined(separator: "  ·  ")
    }

    private var healthLabel: String {
        switch current.health?.status {
        case "healthy": "Healthy"
        case "warning": "Warning"
        case "error": "Needs fixing"
        default: "Unknown"
        }
    }

    // MARK: Today

    private func todayCard(_ usage: AnalyticsAccountUsage) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            CardTitle("Today", caption: "Sends against this mailbox's daily limits")
            usageRow("Campaign", symbol: "paperplane.fill", sent: usage.campaignSent ?? 0, limit: usage.campaignLimit ?? 0, color: WTheme.accent, delay: 0.2)
            if let warmupLimit = usage.warmupLimit, warmupLimit > 0 {
                usageRow("Warmup", symbol: "flame.fill", sent: usage.warmupSent ?? 0, limit: warmupLimit, color: Tone.orange.color, delay: 0.3)
            }
        }
        .coverCard()
    }

    private func usageRow(_ label: String, symbol: String, sent: Int, limit: Int, color: Color, delay: Double) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: symbol)
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(color)
                    .frame(width: 18)
                Text(label)
                    .font(.system(size: 15))
                    .foregroundStyle(WTheme.ink)
                Spacer()
                Text("\(Text("\(sent)").foregroundStyle(WTheme.ink).fontWeight(.semibold)) of \(limit)")
                    .font(.system(size: 14))
                    .monospacedDigit()
                    .foregroundStyle(WTheme.inkSecondary)
            }
            GrowBar(fraction: limit > 0 ? Double(sent) / Double(limit) : 0, color: color, shown: shown, delay: delay, height: 8)
                .padding(.leading, 26)
        }
        .accessibilityElement(children: .combine)
    }

    // MARK: Warmup

    private var warmupCard: some View {
        let status = current.warmupStatus
        let ceiling = max(1, status?.maxVolume ?? 40)
        let today = min(ceiling, status?.targetVolume ?? 0)
        let paused = status?.paused == true
        let health = current.warmupHealth
        return VStack(alignment: .leading, spacing: 16) {
            CardTitle(title: "Warmup", caption: paused ? "Paused" : "Day \(status?.daysActive ?? 0) of the ramp") {
                if let state = health?.state {
                    StatusPill(text: Self.standing(state), tone: AnalyticsToneMap.warmupState(state))
                }
            }
            VStack(alignment: .leading, spacing: 8) {
                WarmupBar(progress: shown ? Double(today) / Double(ceiling) : 0, paused: paused)
                    .frame(height: 10)
                HStack {
                    Text("\(Text("\(today)").foregroundStyle(WTheme.ink).fontWeight(.semibold)) a day now")
                    Spacer()
                    Text("ceiling \(ceiling)")
                }
                .font(.system(size: 13))
                .monospacedDigit()
                .foregroundStyle(WTheme.inkSecondary)
            }
            HStack(spacing: 0) {
                AnalyticsMiniStat(label: "Sent today", value: "\(status?.currentVolume ?? 0)")
                AnalyticsMiniStat(label: "Reply rate", value: "\(status?.replyRate ?? 0)%")
                AnalyticsMiniStat(label: "Days", value: "\(status?.daysActive ?? 0)")
            }
            if let reason = health?.reason, !reason.isEmpty {
                Text(reason)
                    .font(.system(size: 13.5))
                    .foregroundStyle(WTheme.inkSecondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let until = health?.blockedUntil {
                Label("Held until \(until.formatted(.dateTime.month(.abbreviated).day().hour().minute()))", systemImage: "clock")
                    .font(.system(size: 13.5, weight: .medium))
                    .foregroundStyle(WTheme.negative)
            }
        }
        .coverCard()
    }

    static func standing(_ state: String) -> String {
        switch state {
        case "healthy": "Good standing"
        case "watch": "Watched"
        case "throttled": "Slowed down"
        case "quarantined": "Quarantined"
        case "blocked": "Blocked"
        default: state.prefix(1).uppercased() + state.dropFirst()
        }
    }

    // MARK: Placement

    private func placementCard(_ placement: AnalyticsPlacement) -> some View {
        let delivered = max(1, placement.delivered ?? 1)
        let parts: [(String, Int, Color)] = [
            ("Inbox", placement.inbox ?? 0, WTheme.positive),
            ("Tabs", placement.tabs ?? 0, WTheme.accent),
            ("Spam", placement.spam ?? 0, WTheme.negative),
        ]
        let rate = placement.inboxRate ?? 0
        return VStack(alignment: .leading, spacing: 14) {
            CardTitle(title: "Where warmup landed", caption: "\(placement.delivered ?? 0) verified deliveries, last \(placement.windowDays ?? 7) days") {
                Text(String(format: "%.0f%%", rate))
                    .font(.system(size: 22, weight: .semibold, design: .rounded))
                    .monospacedDigit()
                    .foregroundStyle(rate >= 90 ? WTheme.positive : rate >= 80 ? WTheme.warning : WTheme.negative)
            }
            GeometryReader { proxy in
                HStack(spacing: 3) {
                    ForEach(Array(parts.enumerated()), id: \.offset) { index, part in
                        if part.1 > 0 {
                            RoundedRectangle(cornerRadius: 4, style: .continuous)
                                .fill(part.2)
                                .frame(width: shown ? max(6, (proxy.size.width - 6) * CGFloat(part.1) / CGFloat(delivered)) : 0)
                                .animation(.spring(response: 0.9, dampingFraction: 0.8).delay(0.25 + Double(index) * 0.1), value: shown)
                        }
                    }
                    Spacer(minLength: 0)
                }
            }
            .frame(height: 12)
            HStack(spacing: 0) {
                ForEach(Array(parts.enumerated()), id: \.offset) { _, part in
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Circle().fill(part.2).frame(width: 7, height: 7)
                            Text(part.0)
                                .font(.system(size: 13))
                                .foregroundStyle(WTheme.inkSecondary)
                        }
                        Text("\(part.1)")
                            .font(.system(size: 18, weight: .semibold, design: .rounded))
                            .monospacedDigit()
                            .foregroundStyle(WTheme.ink)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
        .coverCard()
    }

    // MARK: Errors

    private func errorsCard(_ errors: [AnalyticsAccountError]) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            CardTitle("Errors").padding(.bottom, 8)
            ForEach(Array(errors.enumerated()), id: \.element.id) { index, item in
                if index > 0 { Hairline().padding(.leading, 46) }
                HStack(alignment: .top, spacing: 12) {
                    SettingsIcon(symbol: "exclamationmark.triangle.fill", color: severityTone(item.severity).color, size: 34)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(item.title ?? item.errorCode ?? "Error")
                            .font(.system(size: 15, weight: .medium))
                            .foregroundStyle(WTheme.ink)
                        if let message = item.message, !message.isEmpty {
                            Text(message)
                                .font(.system(size: 13.5))
                                .foregroundStyle(WTheme.inkSecondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                        if let action = item.actionRequired, !action.isEmpty {
                            Text(action)
                                .font(.system(size: 13.5, weight: .medium))
                                .foregroundStyle(WTheme.warning)
                        }
                    }
                    Spacer(minLength: 4)
                    if let created = item.createdAt {
                        Text(WFormat.ago(created))
                            .font(.system(size: 13))
                            .monospacedDigit()
                            .foregroundStyle(WTheme.inkTertiary)
                    }
                }
                .padding(.vertical, 10)
            }
        }
        .coverCard()
    }

    private func severityTone(_ raw: String?) -> Tone {
        switch raw {
        case "critical", "error": .rose
        case "warning": .amber
        default: .slate
        }
    }
}
