import SwiftUI

/// Totals over a run of days, with the rates the KPIs show.
struct HomeWindow {
    var sent = 0
    var opens = 0
    var clicks = 0
    var replies = 0

    init(_ days: some Sequence<TrendDay>) {
        for day in days {
            sent += day.sent
            opens += day.opens
            clicks += day.clicks
            replies += day.replies
        }
    }

    var openRate: Double? { sent > 0 ? Double(opens) / Double(sent) * 100 : nil }
    var replyRate: Double? { sent > 0 ? Double(replies) / Double(sent) * 100 : nil }
    var clickRate: Double? { sent > 0 ? Double(clicks) / Double(sent) * 100 : nil }
}

/// Home pulse: the week's dashboard, a two-week daily trend for the deltas,
/// the mailbox fleet, and what is due today. Sections degrade when a
/// permission is missing.
@MainActor
@Observable
final class HomeStore {
    private(set) var dashboard: DashboardAnalytics?
    private(set) var days: [TrendDay] = []
    private(set) var accounts: [EmailAccount] = []
    private(set) var statuses: [String: AccountAnalytics] = [:]
    private(set) var accountTotal: Int?
    private(set) var tasks: [CRMTask] = []
    private(set) var meetings: [CRMMeeting] = []
    private(set) var hasLoaded = false

    var warmingCount: Int { accounts.filter(\.isWarmingActive).count }
    var issueCount: Int {
        accounts.filter { statuses[$0.id]?.health?.hasIssue == true }.count
    }

    /// The last seven days and the seven before them.
    var thisWeek: HomeWindow { HomeWindow(days.suffix(7)) }
    var lastWeek: HomeWindow { HomeWindow(days.prefix(max(0, days.count - 7))) }
    var hasTrend: Bool { days.contains { $0.sent > 0 || $0.replies > 0 } }

    /// Mailboxes with problems first, so the short list shows what matters.
    var accountsByUrgency: [EmailAccount] {
        accounts.sorted { a, b in
            let ia = statuses[a.id]?.health?.hasIssue == true
            let ib = statuses[b.id]?.health?.hasIssue == true
            if ia != ib { return ia }
            return (statuses[a.id]?.health?.score ?? 100) < (statuses[b.id]?.health?.score ?? 100)
        }
    }

    func load(_ api: APIClient, analytics: Bool, emails: Bool, crm: Bool) async {
        async let dashboardTask: DashboardAnalytics? = analytics
            ? try? api.get("analytics/dashboard", query: ["period": "7d"])
            : nil
        async let monthTask: DashboardAnalytics? = analytics
            ? try? api.get("analytics/dashboard", query: ["period": "30d"])
            : nil
        async let accountsTask: ListResponse<EmailAccount>? = emails
            ? try? api.get("emails", query: ["limit": "200"])
            : nil
        async let statusTask: ListResponse<AccountAnalytics>? = analytics
            ? try? api.get("analytics/accounts")
            : nil
        async let tasksTask: CRMPage<CRMTask>? = crm
            ? try? api.post("crm/tasks/search", body: CRMTaskSearchBody(sortBy: "due_date"), query: ["limit": "100"])
            : nil
        async let meetingsTask: CRMPage<CRMMeeting>? = crm
            ? try? api.get("meetings", query: ["limit": "30", "timeframe": "upcoming"])
            : nil

        let (dash, month, accountPage, statusPage, taskPage, meetingPage) = await (
            dashboardTask, monthTask, accountsTask, statusTask, tasksTask, meetingsTask
        )
        let endOfToday = Calendar.current.startOfDay(for: .now).addingTimeInterval(86400)
        withAnimation(.snappy) {
            dashboard = dash
            days = Self.lastFourteenDays(month?.dailyTrend ?? dash?.dailyTrend ?? [])
            if let accountPage {
                accounts = accountPage.data
                accountTotal = (accountPage.pagination?.total).map(Int.init) ?? accountPage.data.count
            }
            if let statusPage {
                statuses = Dictionary(statusPage.data.map { ($0.id, $0) }, uniquingKeysWith: { _, latest in latest })
            }
            if let taskPage {
                tasks = taskPage.items
                    .filter { !$0.isDone && ($0.dueDate ?? .distantFuture) < endOfToday }
                    .sorted { ($0.dueDate ?? .distantFuture) < ($1.dueDate ?? .distantFuture) }
            }
            if let meetingPage {
                meetings = meetingPage.items
                    .filter { $0.isToday && $0.status != "canceled" }
                    .sorted { ($0.scheduledFor ?? .distantFuture) < ($1.scheduledFor ?? .distantFuture) }
            }
            hasLoaded = true
        }
    }

    /// The trend only lists days with data; fill a continuous fortnight.
    private static func lastFourteenDays(_ points: [AnalyticsTrendPoint]) -> [TrendDay] {
        var values: [String: TrendDay] = [:]
        for point in points {
            guard let raw = point.date else { continue }
            values[String(raw.prefix(10))] = TrendDay(
                date: .now,
                sent: point.sent ?? 0,
                opens: point.opens ?? 0,
                clicks: point.clicks ?? 0,
                replies: point.replies ?? 0
            )
        }
        return TrendDay.lastDays(14, values: values)
    }
}

// MARK: - Pieces

/// A smooth line through the values that draws itself in, with a soft fill.
struct Sparkline: View {
    let values: [Double]
    var color: Color = WTheme.accent
    var fill = true
    var lineWidth: CGFloat = 1.7
    var delay: Double = 0

    @State private var drawn: CGFloat = 0

    var body: some View {
        GeometryReader { proxy in
            let line = Self.path(values, in: proxy.size)
            ZStack {
                if fill {
                    Self.area(line, in: proxy.size)
                        .fill(LinearGradient(colors: [color.opacity(0.22), color.opacity(0)], startPoint: .top, endPoint: .bottom))
                        .mask(alignment: .leading) {
                            Rectangle().frame(width: proxy.size.width * drawn)
                        }
                }
                line
                    .trim(from: 0, to: drawn)
                    .stroke(color, style: StrokeStyle(lineWidth: lineWidth, lineCap: .round, lineJoin: .round))
            }
        }
        .onAppear {
            withAnimation(.easeOut(duration: 1.0).delay(delay)) { drawn = 1 }
        }
        .accessibilityHidden(true)
    }

    static func area(_ line: Path, in size: CGSize) -> Path {
        var area = line
        area.addLine(to: CGPoint(x: size.width, y: size.height))
        area.addLine(to: CGPoint(x: 0, y: size.height))
        area.closeSubpath()
        return area
    }

    static func path(_ values: [Double], in size: CGSize) -> Path {
        var path = Path()
        guard values.count > 1 else { return path }
        let maxV = max(values.max() ?? 0, 0.0001)
        let minV = min(values.min() ?? 0, 0)
        let span = max(maxV - minV, 0.0001)
        let inset: CGFloat = 2
        let points = values.enumerated().map { i, v in
            CGPoint(
                x: size.width * CGFloat(i) / CGFloat(values.count - 1),
                y: inset + (size.height - inset * 2) * (1 - CGFloat((v - minV) / span))
            )
        }
        path.move(to: points[0])
        for i in 1 ..< points.count {
            let p0 = points[i - 1], p1 = points[i]
            let mid = (p1.x - p0.x) / 2
            path.addCurve(to: p1, control1: CGPoint(x: p0.x + mid, y: p0.y), control2: CGPoint(x: p1.x - mid, y: p1.y))
        }
        return path
    }
}

/// Week-over-week change: green when it moved the right way.
struct DeltaLabel: View {
    /// Percent change for counts, points for rates.
    let change: Double?
    var points = false
    var lowerIsBetter = false

    var body: some View {
        if let change, change.isFinite {
            let up = change >= 0
            let good = abs(change) < 0.05 ? nil : (up != lowerIsBetter)
            HStack(spacing: 2) {
                Image(systemName: up ? "arrow.up.right" : "arrow.down.right")
                    .font(.system(size: 9, weight: .bold))
                Text(points ? String(format: "%.1f pts", abs(change)) : String(format: "%.0f%%", abs(change)))
                    .font(.system(size: 12, weight: .semibold))
                    .monospacedDigit()
            }
            .foregroundStyle(good == nil ? WTheme.inkTertiary : (good! ? WTheme.positive : WTheme.negative))
        } else {
            Text("new")
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(WTheme.inkTertiary)
        }
    }

    static func percent(_ now: Int, _ before: Int) -> Double? {
        guard before > 0 else { return now > 0 ? nil : 0 }
        return (Double(now) - Double(before)) / Double(before) * 100
    }

    static func points(_ now: Double?, _ before: Double?) -> Double? {
        guard let now, let before else { return nil }
        return now - before
    }
}
