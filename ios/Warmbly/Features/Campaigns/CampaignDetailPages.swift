import SwiftUI
import Charts

// Pushed sub-pages of the campaign hub (Leads / Sequence / Senders) plus the
// analytics store the hub's performance section reads. Each page: SkeletonRows
// on first load, stale data during reloads, ErrorStateView on failure,
// EmptyStateView when empty, and a reload on the relevant realtime pulse.
// Heavy editing (writing steps, assigning senders) is a web-dashboard job;
// those pages carry a WebHandoffBanner instead of half an editor.

// MARK: - Analytics store (hub performance section)

@MainActor
@Observable
final class CampaignOverviewStore {
    private(set) var analytics: CampaignAnalytics?
    /// The last 90 UTC days, continuous; the trend picker slices it.
    private(set) var days: [TrendDay] = []
    private(set) var leadCounts: CampaignLeadCounts?
    private(set) var isLoading = false
    private(set) var errorMessage: String?
    private(set) var hasLoaded = false

    func load(_ api: APIClient, campaignID: String) async {
        isLoading = true
        if !hasLoaded { errorMessage = nil }
        let today = TrendDay.utc.startOfDay(for: .now)
        let from = TrendDay.utc.date(byAdding: .day, value: -89, to: today)!
        async let summaryTask: CampaignAnalytics = api.get("analytics/campaigns/\(campaignID)")
        async let dailyTask: CampaignDailyPage? = try? api.get(
            "analytics/campaigns/\(campaignID)/daily",
            query: ["from": TrendDay.key.string(from: from), "to": TrendDay.key.string(from: today)]
        )
        async let countsTask = CampaignsStore.leadCounts(api, campaignID: campaignID)
        do {
            let result = try await summaryTask
            let dailyPage = await dailyTask
            let counts = await countsTask
            var values: [String: TrendDay] = [:]
            for day in dailyPage?.data ?? [] {
                guard let raw = day.date else { continue }
                values[String(raw.prefix(10))] = TrendDay(
                    date: .now,
                    sent: day.sent ?? 0,
                    opens: day.opens ?? 0,
                    clicks: day.clicks ?? 0,
                    replies: day.replies ?? 0
                )
            }
            withAnimation(.snappy) {
                analytics = result
                days = TrendDay.lastDays(90, values: values)
                if let counts { leadCounts = counts }
            }
            errorMessage = nil
            hasLoaded = true
        } catch {
            if !hasLoaded { errorMessage = error.localizedDescription }
        }
        isLoading = false
    }
}

/// How many days the campaign trend shows.
enum CampaignTrendRange: Int, CaseIterable, Identifiable {
    case week = 7
    case fortnight = 14
    case month = 30
    case quarter = 90

    var id: Int { rawValue }
    var label: String { "\(rawValue)d" }
    var caption: String { "Last \(rawValue) days" }
}

/// The campaign's sends, opens and replies over a range you pick, in the same
/// scrubbable chart as Home. The range is remembered on the device.
struct CampaignTrendSection: View {
    let days: [TrendDay]
    var shown: Bool

    @AppStorage("campaign.trend.range") private var rangeRaw = CampaignTrendRange.fortnight.rawValue
    @Namespace private var pill

    private var range: CampaignTrendRange { CampaignTrendRange(rawValue: rangeRaw) ?? .fortnight }
    private var visible: [TrendDay] { Array(days.suffix(range.rawValue)) }
    private var isQuiet: Bool { days.allSatisfy { $0.sent == 0 && $0.opens == 0 && $0.replies == 0 } }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                EyebrowLabel("Activity")
                Spacer()
                if !isQuiet { picker }
            }
            if isQuiet {
                quiet
            } else {
                TrendChart(days: visible, shown: shown, caption: range.caption)
            }
        }
        .sensoryFeedback(.selection, trigger: rangeRaw)
    }

    private var picker: some View {
        HStack(spacing: 2) {
            ForEach(CampaignTrendRange.allCases) { option in
                let active = option == range
                Button {
                    withAnimation(.spring(response: 0.35, dampingFraction: 0.82)) { rangeRaw = option.rawValue }
                } label: {
                    Text(option.label)
                        .font(.system(size: 13, weight: active ? .semibold : .medium))
                        .monospacedDigit()
                        .foregroundStyle(active ? WTheme.ink : WTheme.inkTertiary)
                        .padding(.horizontal, 10)
                        .frame(height: 28)
                        .background {
                            if active {
                                Capsule()
                                    .fill(WTheme.raised)
                                    .shadow(color: .black.opacity(0.08), radius: 3, y: 1)
                                    .matchedGeometryEffect(id: "range", in: pill)
                            }
                        }
                        .contentShape(Capsule())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(active ? .isSelected : [])
            }
        }
        .padding(2)
        .background(WTheme.fill, in: Capsule())
    }

    private var quiet: some View {
        VStack(spacing: 8) {
            Image(systemName: "chart.line.uptrend.xyaxis")
                .font(.system(size: 22, weight: .medium))
                .foregroundStyle(WTheme.inkTertiary)
            Text("Sends, opens and replies chart here once the first emails go out.")
                .font(.system(size: 14))
                .multilineTextAlignment(.center)
                .foregroundStyle(WTheme.inkSecondary)
        }
        .frame(maxWidth: .infinity)
        .frame(height: 150)
        .background(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .strokeBorder(WTheme.hairlineStrong, style: StrokeStyle(lineWidth: 1, dash: [4, 4]))
        )
    }
}

// MARK: - Sequence

@MainActor
@Observable
final class CampaignStepsStore {
    private(set) var steps: [CampaignStep] = []
    private(set) var isLoading = false
    private(set) var errorMessage: String?
    private(set) var hasLoaded = false

    func load(_ api: APIClient, campaignID: String) async {
        isLoading = true
        if !hasLoaded { errorMessage = nil }
        do {
            // GET /campaigns/:id/steps returns a raw JSON array (no envelope).
            let result: [CampaignStep] = try await api.get("campaigns/\(campaignID)/steps")
            withAnimation(.snappy) {
                steps = result.sorted { ($0.position ?? 0) < ($1.position ?? 0) }
            }
            errorMessage = nil
            hasLoaded = true
        } catch {
            if !hasLoaded { errorMessage = error.localizedDescription }
        }
        isLoading = false
    }
}

/// The sequence as a task timeline: a hairline rail with numbered nodes,
/// each step a hairline card that opens the read-only step viewer.
struct CampaignSequencePage: View {
    @Environment(AppEnvironment.self) private var env
    let campaignID: String
    let store: CampaignStepsStore

    var body: some View {
        Group {
            if store.isLoading, !store.hasLoaded {
                ScrollView { SkeletonRows(rows: 5) }
            } else if let error = store.errorMessage, store.steps.isEmpty {
                ErrorStateView(title: "Couldn't load steps", message: error) {
                    await store.load(env.api, campaignID: campaignID)
                }
            } else if store.steps.isEmpty {
                EmptyStateView(
                    title: "No steps yet",
                    message: "Write the sequence in Warmbly on the web; every step shows up here.",
                    symbol: "list.number"
                )
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        header
                            .padding(.bottom, 18)
                        LazyVStack(alignment: .leading, spacing: 0) {
                            ForEach(Array(store.steps.enumerated()), id: \.element.id) { index, step in
                                NavigationLink {
                                    CampaignStepReader(step: step, all: store.steps)
                                } label: {
                                    CampaignStepRow(
                                        step: step,
                                        all: store.steps,
                                        isFirst: index == 0,
                                        isLast: index == store.steps.count - 1
                                    )
                                }
                                .buttonStyle(TapScaleStyle())
                            }
                        }
                        WebHandoffBanner(text: "Writing and rearranging steps needs the full sequence editor. Open this campaign in Warmbly on the web to edit it.")
                            .padding(.top, 22)
                    }
                    .padding(.horizontal, 20)
                    .padding(.top, 12)
                    .padding(.bottom, 32)
                }
                .refreshable { await store.load(env.api, campaignID: campaignID) }
            }
        }
        .background(WTheme.canvas)
        .navigationTitle("Sequence")
        .navigationBarTitleDisplayMode(.inline)
        .task { if !store.hasLoaded { await store.load(env.api, campaignID: campaignID) } }
        .onChange(of: env.realtime.pulse(for: .campaigns)) {
            Task { await store.load(env.api, campaignID: campaignID) }
        }
    }

    private var header: some View {
        let count = store.steps.count
        let emails = store.steps.filter(\.isEmail).count
        return VStack(alignment: .leading, spacing: 8) {
            EyebrowLabel("\(count) step\(count == 1 ? "" : "s") · \(emails) email\(emails == 1 ? "" : "s")")
            TwoToneTitle(lead: "Every touch,", title: "in order.", size: 26)
        }
    }
}

/// Read-only presentation for sequence step nodes: kind icon/tone, action
/// summaries, and human-readable branch routes (mirrors the web editor's copy).
enum StepMeta {
    static func tone(_ step: CampaignStep) -> Tone {
        switch step.stepKind {
        case "wait": return .amber
        case "action": return .indigo
        default: return .sky
        }
    }

    static func icon(_ step: CampaignStep) -> String {
        switch step.stepKind {
        case "wait": return "hourglass"
        case "action": return actionIcon(step.action?.type)
        default: return "envelope"
        }
    }

    static func title(_ step: CampaignStep) -> String {
        if let name = step.name, !name.isEmpty { return name }
        switch step.stepKind {
        case "wait": return "Wait"
        case "action": return "Action"
        default: return "Step"
        }
    }

    private static func actionIcon(_ type: String?) -> String {
        switch type {
        case "wait": return "hourglass"
        case "add_tag", "remove_tag": return "tag"
        case "label_email": return "tray.full"
        case "unsubscribe": return "person.fill.xmark"
        case "notify": return "bell"
        case "create_task": return "checklist"
        case "create_deal", "move_deal_stage": return "briefcase"
        case "run_automation": return "gearshape.2"
        case "fire_event": return "dot.radiowaves.left.and.right"
        case "end": return "stop.circle"
        default: return "bolt"
        }
    }

    /// One-line summary of what a non-email node does when a contact hits it.
    static func actionSummary(_ step: CampaignStep) -> String {
        let action = step.action
        switch action?.type {
        case "wait":
            if let minutes = action?.waitMinutes, minutes > 0 { return "Waits \(duration(minutes))" }
            return "Waits before the next step"
        case "add_tag": return "Adds a tag to the contact"
        case "remove_tag": return "Removes a tag from the contact"
        case "label_email": return "Labels the reply conversation"
        case "unsubscribe": return "Unsubscribes the contact"
        case "notify": return "Notifies your team"
        case "create_task":
            if let title = action?.taskTitle, !title.isEmpty { return "Creates task \"\(title)\"" }
            return "Creates a CRM task"
        case "create_deal":
            if let name = action?.dealName, !name.isEmpty { return "Creates deal \"\(name)\"" }
            return "Creates a CRM deal"
        case "move_deal_stage": return "Moves the contact's deal to another stage"
        case "run_automation": return "Runs an automation"
        case "fire_event":
            if let name = action?.eventName, !name.isEmpty { return "Fires event \"\(name)\"" }
            return "Fires a custom event"
        case "end": return "Ends the sequence"
        default:
            if step.stepKind == "wait" { return "Waits before the next step" }
            return "Runs an action"
        }
    }

    static func duration(_ minutes: Int) -> String {
        if minutes % 1440 == 0 {
            let days = minutes / 1440
            return "\(days) day\(days == 1 ? "" : "s")"
        }
        if minutes % 60 == 0 {
            let hours = minutes / 60
            return "\(hours) hour\(hours == 1 ? "" : "s")"
        }
        return "\(minutes) min"
    }

    static func conditionText(_ cond: StepBranchCondition) -> String {
        let field = cond.field ?? ""
        if field == "random" {
            if let value = cond.value { return "random split (\(value)%)" }
            return "random split"
        }
        let label: String
        switch field {
        case "opened": label = "opened the email"
        case "clicked": label = "clicked a link"
        case "replied": label = "replied"
        case "not_opened": label = "didn't open"
        case "not_clicked": label = "didn't click"
        case "not_replied": label = "didn't reply"
        case "reply_positive": label = "replied: positive"
        case "reply_negative": label = "replied: negative"
        case "reply_neutral": label = "replied: neutral"
        case "reply_automated": label = "auto-reply / out of office"
        default: label = field.replacingOccurrences(of: "_", with: " ")
        }
        if cond.op == "within_days", let value = cond.value {
            return "\(label) within \(value) day\(value == 1 ? "" : "s")"
        }
        return label
    }

    /// "If opened the email within 3 days → Step 4" / "Otherwise → stops".
    static func branchText(_ branch: StepBranch, in all: [CampaignStep]) -> String {
        let conditions = (branch.conditions ?? []).map(conditionText)
        let lhs = conditions.isEmpty ? "Otherwise" : "If " + conditions.joined(separator: " and ")
        let rhs: String
        if let target = branch.targetStepID,
           let step = all.first(where: { $0.id == target }) {
            rhs = "goes to step \(step.position ?? 0)"
        } else {
            rhs = "stops the sequence"
        }
        return "\(lhs), \(rhs)"
    }
}

/// One node of the sequence timeline: the rail segment with a mono-numbered
/// node on the left, the step as a hairline card on the right.
struct CampaignStepRow: View {
    let step: CampaignStep
    let all: [CampaignStep]
    var isFirst = false
    var isLast = false

    private static let railWidth: CGFloat = 26
    /// Node centre sits on the card's first line.
    private static let nodeTop: CGFloat = 12

    private var waitLine: String? {
        guard let wait = step.waitAfter, wait > 0 else { return nil }
        return "wait \(wait)d"
    }

    var body: some View {
        card
            .padding(.leading, Self.railWidth + 12)
            .padding(.bottom, isLast ? 0 : 12)
            .background(alignment: .topLeading) { rail }
    }

    private var rail: some View {
        VStack(spacing: 0) {
            Rectangle()
                .fill(isFirst ? Color.clear : WTheme.hairlineStrong)
                .frame(width: 1, height: Self.nodeTop)
            Text("\(step.position ?? 0)")
                .font(WFont.meta(11, weight: .medium))
                .foregroundStyle(WTheme.ink)
                .frame(width: Self.railWidth, height: Self.railWidth)
                .background(WTheme.raised, in: Circle())
                .overlay(Circle().strokeBorder(WTheme.hairlineStrong, lineWidth: 1))
            Rectangle()
                .fill(isLast ? Color.clear : WTheme.hairlineStrong)
                .frame(width: 1)
                .frame(maxHeight: .infinity)
        }
        .frame(width: Self.railWidth)
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                StepKindChip(step: step)
                Spacer(minLength: 8)
                if let wait = waitLine {
                    Text(wait)
                        .font(WFont.meta(11))
                        .foregroundStyle(WTheme.inkTertiary)
                }
                Image(systemName: "chevron.right")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(WTheme.inkTertiary)
            }
            Text(StepMeta.title(step))
                .font(.system(size: 15.5, weight: .medium))
                .tracking(-0.2)
                .foregroundStyle(WTheme.ink)
                .lineLimit(1)
                .padding(.top, 2)
            if step.isEmail {
                if let subject = step.subject, !subject.isEmpty {
                    Text(subject)
                        .font(.system(size: 14))
                        .foregroundStyle(WTheme.inkSecondary)
                        .lineLimit(1)
                }
                let preview = step.bodyPreview
                if !preview.isEmpty {
                    Text(preview)
                        .font(.system(size: 13.5))
                        .foregroundStyle(WTheme.inkTertiary)
                        .lineLimit(2)
                }
            } else {
                Text(StepMeta.actionSummary(step))
                    .font(.system(size: 14))
                    .foregroundStyle(WTheme.inkSecondary)
                    .lineLimit(1)
            }
            if !step.branches.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(Array(step.branches.enumerated()), id: \.offset) { _, branch in
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            Image(systemName: "arrow.triangle.branch")
                                .font(.system(size: 10, weight: .medium))
                                .foregroundStyle(WTheme.inkTertiary)
                            Text(StepMeta.branchText(branch, in: all))
                                .font(.system(size: 12.5))
                                .foregroundStyle(WTheme.inkSecondary)
                                .lineLimit(1)
                        }
                    }
                }
                .padding(.top, 8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .overlay(alignment: .top) { Hairline() }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(WTheme.raised, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .strokeBorder(WTheme.hairlineStrong, lineWidth: 1)
        )
        .contentShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
    }
}

/// Mono kind label (email / wait / action) with a tone-colored glyph.
private struct StepKindChip: View {
    let step: CampaignStep

    private var label: String {
        switch step.stepKind {
        case "wait": "Wait"
        case "action": "Action"
        default: "Email"
        }
    }

    var body: some View {
        HStack(spacing: 5) {
            Image(systemName: StepMeta.icon(step))
                .font(.system(size: 9.5, weight: .medium))
                .foregroundStyle(StepMeta.tone(step).color)
            Text(label)
                .font(WFont.meta(10, weight: .medium))
                .foregroundStyle(WTheme.inkSecondary)
        }
        .padding(.horizontal, 8)
        .frame(height: 20)
        .background(WTheme.fill, in: Capsule())
    }
}

/// Read-only step viewer. Email nodes show the subject and the full body
/// exactly as it sends (HTML through the shared webview, plain text as a
/// fallback); action/wait nodes show what fires, and every node lists its
/// branch routes.
struct CampaignStepReader: View {
    let step: CampaignStep
    let all: [CampaignStep]

    @State private var bodyHeight: CGFloat = 120

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                VStack(alignment: .leading, spacing: 10) {
                    HStack(spacing: 8) {
                        StepKindChip(step: step)
                        EyebrowLabel("Step \(step.position ?? 0)")
                    }
                    if step.isEmail, let subject = step.subject, !subject.isEmpty {
                        Text(subject)
                            .displayFont(22)
                            .foregroundStyle(WTheme.ink)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    if let wait = step.waitAfter, wait > 0 {
                        Text("\(step.isEmail ? "Sends" : "Runs") \(wait) day\(wait == 1 ? "" : "s") after the previous step")
                            .font(WFont.meta(11.5))
                            .foregroundStyle(WTheme.inkTertiary)
                    }
                }
                Hairline()
                if step.isEmail {
                    if step.bodyHTML?.isEmpty == false || step.bodyPlain?.isEmpty == false {
                        UniboxWebView(html: step.bodyHTML, plain: step.bodyPlain, height: $bodyHeight)
                            .frame(height: bodyHeight)
                    } else {
                        Text("This step has no body yet.")
                            .font(.system(size: 14.5))
                            .foregroundStyle(WTheme.inkSecondary)
                    }
                } else {
                    HStack(spacing: 12) {
                        Image(systemName: StepMeta.icon(step))
                            .font(.system(size: 14))
                            .foregroundStyle(StepMeta.tone(step).color)
                            .frame(width: 36, height: 36)
                            .background(WTheme.raised, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                            .overlay(
                                RoundedRectangle(cornerRadius: 10, style: .continuous)
                                    .strokeBorder(WTheme.hairlineStrong, lineWidth: 1)
                            )
                        VStack(alignment: .leading, spacing: 2) {
                            Text(StepMeta.actionSummary(step))
                                .font(.system(size: 15.5, weight: .medium))
                                .foregroundStyle(WTheme.ink)
                            Text(step.stepKind == "wait" ? "Delay step, nothing is sent." : "Runs automatically, nothing is sent.")
                                .font(.system(size: 13))
                                .foregroundStyle(WTheme.inkSecondary)
                        }
                    }
                }
                if !step.branches.isEmpty {
                    VStack(alignment: .leading, spacing: 10) {
                        EyebrowLabel("Branching")
                        VStack(alignment: .leading, spacing: 0) {
                            ForEach(Array(step.branches.enumerated()), id: \.offset) { index, branch in
                                if index > 0 { Hairline() }
                                HStack(alignment: .firstTextBaseline, spacing: 8) {
                                    Image(systemName: "arrow.triangle.branch")
                                        .font(.system(size: 11, weight: .medium))
                                        .foregroundStyle(WTheme.inkTertiary)
                                    Text(StepMeta.branchText(branch, in: all))
                                        .font(.system(size: 14))
                                        .foregroundStyle(WTheme.ink)
                                        .fixedSize(horizontal: false, vertical: true)
                                    Spacer(minLength: 0)
                                }
                                .padding(.vertical, 11)
                            }
                        }
                        .padding(.horizontal, 14)
                        .background(WTheme.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                        .overlay(
                            RoundedRectangle(cornerRadius: 14, style: .continuous)
                                .strokeBorder(WTheme.hairlineStrong, lineWidth: 1)
                        )
                        Text("If no branch matches, the contact continues to the next step.")
                            .font(.system(size: 12.5))
                            .foregroundStyle(WTheme.inkTertiary)
                    }
                    .padding(.top, 4)
                }
                WebHandoffBanner(text: "Edit this step in the sequence editor in Warmbly on the web.")
                    .padding(.top, 6)
            }
            .padding(.horizontal, 20)
            .padding(.top, 12)
            .padding(.bottom, 32)
        }
        .background(WTheme.canvas)
        .navigationTitle(StepMeta.title(step))
        .navigationBarTitleDisplayMode(.inline)
    }
}

// MARK: - Senders

@MainActor
@Observable
final class CampaignSendersStore {
    private(set) var senders: [CampaignSender] = []
    /// email_account_id -> address, resolved from GET /emails.
    private(set) var addresses: [String: CampaignSenderAccount] = [:]
    private(set) var isLoading = false
    private(set) var errorMessage: String?
    private(set) var hasLoaded = false

    func load(_ api: APIClient, campaignID: String) async {
        isLoading = true
        if !hasLoaded { errorMessage = nil }
        do {
            let page: CampaignSendersPage = try await api.get("campaigns/\(campaignID)/senders")
            let list = page.data ?? []
            // Resolve account ids to addresses; best-effort, never blocks the list.
            var lookup: [String: CampaignSenderAccount] = [:]
            if !list.isEmpty,
               let accounts: CampaignSenderAccountsPage = try? await api.get("emails", query: ["limit": "200"]) {
                for account in accounts.data ?? [] { lookup[account.id] = account }
            }
            withAnimation(.snappy) {
                senders = list
                addresses = lookup
            }
            errorMessage = nil
            hasLoaded = true
        } catch {
            if !hasLoaded { errorMessage = error.localizedDescription }
        }
        isLoading = false
    }
}

struct CampaignSendersPageView: View {
    @Environment(AppEnvironment.self) private var env
    let campaign: Campaign
    let store: CampaignSendersStore

    private var strategyLine: String {
        campaign.senderStrategy == "tags"
            ? "Sends from mailboxes matching the campaign's tags."
            : "Sends from the mailboxes listed here."
    }

    var body: some View {
        Group {
            if store.isLoading, !store.hasLoaded {
                ScrollView { SkeletonRows(rows: 5) }
            } else if let error = store.errorMessage, store.senders.isEmpty {
                ErrorStateView(title: "Couldn't load senders", message: error) {
                    await store.load(env.api, campaignID: campaign.id)
                }
            } else if store.senders.isEmpty {
                EmptyStateView(
                    title: "No senders assigned",
                    message: campaign.senderStrategy == "tags"
                        ? "This campaign sends from tagged mailboxes. Assign tags on the web dashboard."
                        : "Assign sender mailboxes on the web dashboard.",
                    symbol: "tray.full"
                )
            } else {
                List {
                    VStack(alignment: .leading, spacing: 6) {
                        EyebrowLabel("\(store.senders.count) mailbox\(store.senders.count == 1 ? "" : "es")")
                        Text(strategyLine)
                            .font(.system(size: 13.5))
                            .foregroundStyle(WTheme.inkSecondary)
                    }
                    .padding(.top, 8)
                    .padding(.bottom, 6)
                    .listRowSeparator(.hidden)
                    .listRowBackground(WTheme.canvas)
                    .listRowInsets(EdgeInsets(top: 4, leading: 20, bottom: 4, trailing: 20))
                    ForEach(store.senders) { sender in
                        CampaignSenderRow(sender: sender, account: store.addresses[sender.emailAccountID])
                            .listRowBackground(WTheme.canvas)
                            .listRowSeparatorTint(WTheme.hairline)
                            .listRowInsets(EdgeInsets(top: 4, leading: 20, bottom: 4, trailing: 20))
                            .alignmentGuide(.listRowSeparatorLeading) { _ in 68 }
                    }
                    WebHandoffBanner(text: "Assigning mailboxes, tags and rotation weights happens in the campaign preferences in Warmbly on the web.")
                        .listRowSeparator(.hidden)
                        .listRowBackground(WTheme.canvas)
                        .listRowInsets(EdgeInsets(top: 18, leading: 20, bottom: 24, trailing: 20))
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
                .refreshable { await store.load(env.api, campaignID: campaign.id) }
            }
        }
        .background(WTheme.canvas)
        .navigationTitle("Senders")
        .navigationBarTitleDisplayMode(.inline)
        .task { if !store.hasLoaded { await store.load(env.api, campaignID: campaign.id) } }
        .onChange(of: env.realtime.pulse(for: .emailAccounts)) {
            Task { await store.load(env.api, campaignID: campaign.id) }
        }
    }
}

struct CampaignSenderRow: View {
    let sender: CampaignSender
    let account: CampaignSenderAccount?

    private var title: String {
        account?.email ?? sender.emailAccountID
    }

    private var paused: Bool { sender.enabled == false }

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "envelope")
                .font(.system(size: 14))
                .foregroundStyle(paused ? WTheme.inkTertiary : WTheme.ink)
                .frame(width: 36, height: 36)
                .background(WTheme.raised, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .strokeBorder(WTheme.hairlineStrong, lineWidth: 1)
                )
            VStack(alignment: .leading, spacing: 3) {
                Text(title)
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(paused ? WTheme.inkSecondary : WTheme.ink)
                    .lineLimit(1)
                HStack(spacing: 4) {
                    if let provider = account?.provider, !provider.isEmpty {
                        Text(provider)
                    }
                    if let sent = sender.lastSentAt {
                        if account?.provider?.isEmpty == false { Text("·") }
                        Text("last sent \(WFormat.relative(sent))")
                    }
                }
                .font(WFont.meta(11))
                .foregroundStyle(WTheme.inkTertiary)
                .lineLimit(1)
            }
            Spacer(minLength: 8)
            if paused {
                StatusPill(text: "paused", tone: .slate)
            } else if let weight = sender.weight, weight != 1 {
                Text("×\(weight)")
                    .font(WFont.meta(11.5, weight: .medium))
                    .foregroundStyle(WTheme.inkSecondary)
                    .padding(.horizontal, 8)
                    .frame(height: 22)
                    .background(WTheme.fill, in: Capsule())
            }
        }
        .padding(.vertical, 8)
    }
}
