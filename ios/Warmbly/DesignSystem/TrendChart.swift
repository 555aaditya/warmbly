import Charts
import SwiftUI

/// One UTC day of sending, the unit every trend is reported in.
struct TrendDay: Identifiable {
    let date: Date
    var sent = 0
    var opens = 0
    var clicks = 0
    var replies = 0

    var id: Date { date }

    /// A continuous run of `count` UTC days ending today, zero where `values`
    /// (keyed `yyyy-MM-dd`) has no entry, since trends only list active days.
    static func lastDays(_ count: Int, values: [String: TrendDay]) -> [TrendDay] {
        let today = utc.startOfDay(for: .now)
        return (0 ..< count).map { offset in
            let date = utc.date(byAdding: .day, value: offset - (count - 1), to: today)!
            let value = values[key.string(from: date)]
            return TrendDay(
                date: date,
                sent: value?.sent ?? 0,
                opens: value?.opens ?? 0,
                clicks: value?.clicks ?? 0,
                replies: value?.replies ?? 0
            )
        }
    }

    static let utc: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        return calendar
    }()

    static let key: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.calendar = utc
        formatter.timeZone = utc.timeZone
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter
    }()
}

/// A number that counts up to its value.
struct CountUpText: View, Animatable {
    var value: Double
    var format: (Double) -> String

    var animatableData: Double {
        get { value }
        set { value = newValue }
    }

    var body: some View {
        Text(format(value))
    }
}

// MARK: - Trend

/// Sent, opens and replies in one chart you can scrub: drag across it and the
/// readout shows every metric for that day, a tick per day. The lines draw in
/// on arrival and again whenever the range changes.
struct TrendChart: View {
    let days: [TrendDay]
    var shown: Bool
    var caption = "Last 14 days"

    enum Metric: String, CaseIterable, Identifiable {
        case sent = "Sent"
        case opens = "Opens"
        case replies = "Replies"

        var id: String { rawValue }

        var color: Color {
            switch self {
            case .sent: WTheme.accent
            case .opens: WTheme.special
            case .replies: WTheme.positive
            }
        }

        func value(_ day: TrendDay) -> Int {
            switch self {
            case .sent: day.sent
            case .opens: day.opens
            case .replies: day.replies
            }
        }
    }

    @State private var selected: Date?
    @State private var drawn: CGFloat = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// Days are UTC buckets, so they are named in UTC.
    private static let dayFormat: Date.FormatStyle = {
        var style = Date.FormatStyle.dateTime.weekday(.wide).month(.abbreviated).day()
        style.timeZone = TimeZone(identifier: "UTC")!
        return style
    }()

    private var selectedDay: TrendDay? {
        guard let selected else { return nil }
        var utc = Calendar(identifier: .gregorian)
        utc.timeZone = TimeZone(identifier: "UTC")!
        return days.first { utc.isDate($0.date, inSameDayAs: selected) }
    }

    private var peak: Double {
        Double(days.map { max($0.sent, $0.opens, $0.replies) }.max() ?? 0)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            readout
            chart
        }
        .sensoryFeedback(.selection, trigger: selectedDay?.date)
        .onAppear { if shown { draw(delay: 0.25) } }
        .onChange(of: shown) { _, now in if now { draw(delay: 0.25) } }
        .onChange(of: days.first?.date) {
            guard shown else { return }
            selected = nil
            drawn = 0
            draw(delay: 0.05)
        }
    }

    private func draw(delay: Double) {
        if reduceMotion { drawn = 1; return }
        withAnimation(.easeOut(duration: 1.1).delay(delay)) { drawn = 1 }
    }

    private var lastDay: TrendDay? { days.last }

    private var readout: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top, spacing: 0) {
                ForEach(Metric.allCases) { metric in
                    let value = selectedDay.map(metric.value) ?? days.reduce(0) { $0 + metric.value($1) }
                    VStack(alignment: .leading, spacing: 3) {
                        HStack(spacing: 6) {
                            Circle().fill(metric.color).frame(width: 7, height: 7)
                            Text(metric.rawValue)
                                .font(.system(size: 13.5, weight: .medium))
                                .foregroundStyle(WTheme.inkSecondary)
                        }
                        Text("\(value)")
                            .font(.system(size: 26, weight: .semibold, design: .rounded))
                            .monospacedDigit()
                            .foregroundStyle(WTheme.ink)
                            .contentTransition(.numericText(value: Double(value)))
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            Text(selectedDay.map { Self.dayFormat.format($0.date) } ?? caption)
                .font(.system(size: 13.5))
                .foregroundStyle(WTheme.inkTertiary)
                .contentTransition(.opacity)
        }
        .animation(.snappy(duration: 0.25), value: selectedDay?.date)
        .animation(.snappy(duration: 0.4), value: days.first?.date)
    }

    private var chart: some View {
        Chart {
            ForEach(days) { day in
                AreaMark(
                    x: .value("Day", day.date, unit: .day),
                    y: .value("Count", day.sent),
                    series: .value("Metric", "Sent area")
                )
                .interpolationMethod(.monotone)
                .foregroundStyle(LinearGradient(
                    colors: [Metric.sent.color.opacity(0.22), Metric.sent.color.opacity(0.01)],
                    startPoint: .top,
                    endPoint: .bottom
                ))
            }
            ForEach(Metric.allCases) { metric in
                ForEach(days) { day in
                    LineMark(
                        x: .value("Day", day.date, unit: .day),
                        y: .value("Count", metric.value(day)),
                        series: .value("Metric", metric.rawValue)
                    )
                    .interpolationMethod(.monotone)
                    .foregroundStyle(metric.color)
                    .lineStyle(StrokeStyle(lineWidth: metric == .sent ? 2.5 : 2, lineCap: .round, lineJoin: .round))
                }
            }
            if selectedDay == nil, let today = lastDay, drawn >= 1 {
                // Today is still being written: a soft ping where the line ends.
                PointMark(
                    x: .value("Day", today.date, unit: .day),
                    y: .value("Count", today.sent)
                )
                .symbol { LiveDot(color: Metric.sent.color, size: 8) }
            }
            if let day = selectedDay {
                RuleMark(x: .value("Day", day.date, unit: .day))
                    .foregroundStyle(WTheme.ink.opacity(0.16))
                    .lineStyle(StrokeStyle(lineWidth: 1.5))
                ForEach(Metric.allCases) { metric in
                    PointMark(
                        x: .value("Day", day.date, unit: .day),
                        y: .value("Count", metric.value(day))
                    )
                    .symbolSize(90)
                    .foregroundStyle(metric.color)
                }
            }
        }
        .chartXSelection(value: $selected)
        .chartXAxis {
            AxisMarks(values: .stride(by: .day, count: max(1, days.count / 4))) { _ in
                AxisValueLabel(format: .dateTime.month(.abbreviated).day(), verticalSpacing: 10)
                    .font(.system(size: 11.5))
                    .foregroundStyle(WTheme.inkTertiary)
            }
        }
        .chartYAxis(.hidden)
        .chartYScale(domain: 0 ... max(1, peak * 1.15))
        .chartLegend(.hidden)
        .chartPlotStyle { plot in plot.padding(.top, 8) }
        .frame(height: 180)
        .mask(alignment: .leading) {
            GeometryReader { proxy in
                Rectangle()
                    .frame(width: proxy.size.width * drawn)
            }
        }
    }
}

/// Section entrance: rises in after the hero, one beat apart.
struct Reveal: ViewModifier {
    let shown: Bool
    let index: Int

    func body(content: Content) -> some View {
        content
            .opacity(shown ? 1 : 0)
            .offset(y: shown ? 0 : 18)
            .animation(.spring(response: 0.6, dampingFraction: 0.86).delay(0.08 + Double(index) * 0.06), value: shown)
    }
}

extension View {
    func reveal(_ shown: Bool, _ index: Int) -> some View {
        modifier(Reveal(shown: shown, index: index))
    }
}
