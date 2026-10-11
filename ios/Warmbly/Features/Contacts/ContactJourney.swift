import SwiftUI

/// A contact's way through your outreach: sent, opened, clicked, replied, as
/// four stops on a track that fills to the furthest one reached. The counts
/// roll up and the track draws in when the page opens.
struct ContactJourney: View {
    let engagement: ContactEngagement

    @State private var shown = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private struct Stop: Identifiable {
        let id: String
        let label: String
        let count: Int
        let color: Color
    }

    private var stops: [Stop] {
        [
            Stop(id: "sent", label: "Sent", count: engagement.totalSent ?? 0, color: WTheme.accent),
            Stop(id: "opened", label: "Opened", count: engagement.totalOpened ?? 0, color: WTheme.special),
            Stop(id: "clicked", label: "Clicked", count: engagement.totalClicked ?? 0, color: Tone.orange.color),
            Stop(id: "replied", label: "Replied", count: engagement.totalReplied ?? 0, color: WTheme.positive),
        ]
    }

    /// The furthest stop with anything in it, or nil before the first send.
    private var reached: Int? { stops.lastIndex { $0.count > 0 } }

    static func latest(_ e: ContactEngagement) -> (kind: ContactOutreach.Kind, at: Date)? {
        ContactOutreach(
            lastSentAt: e.lastSentAt, lastOpenedAt: e.lastOpenedAt, lastClickedAt: e.lastClickedAt,
            lastRepliedAt: e.lastRepliedAt, lastBouncedAt: e.lastBouncedAt
        ).latest
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            track
            HStack(spacing: 0) {
                ForEach(Array(stops.enumerated()), id: \.element.id) { index, stop in
                    VStack(spacing: 2) {
                        CountUpText(value: shown ? Double(stop.count) : 0) { WFormat.compact(Int($0.rounded())) }
                            .font(.system(size: 22, weight: .semibold, design: .rounded))
                            .monospacedDigit()
                            .foregroundStyle(stop.count > 0 ? WTheme.ink : WTheme.inkTertiary)
                            .animation(.easeOut(duration: 0.9).delay(0.15 + Double(index) * 0.08), value: shown)
                        Text(stop.label)
                            .font(.system(size: 12.5, weight: .medium))
                            .foregroundStyle(stop.count > 0 ? stop.color : WTheme.inkTertiary)
                    }
                    .frame(maxWidth: .infinity)
                }
            }
            caption
        }
        .padding(.top, 4)
        .onAppear {
            if reduceMotion { shown = true } else { withAnimation { shown = true } }
        }
        .accessibilityElement(children: .combine)
    }

    /// Nodes sit over each column's centre; the line between them fills in the
    /// colours of the stops it passes.
    private var track: some View {
        GeometryReader { proxy in
            let column = proxy.size.width / CGFloat(stops.count)
            let start = column / 2
            let span = proxy.size.width - column
            let progress = reached.map { CGFloat($0) / CGFloat(stops.count - 1) } ?? 0
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(WTheme.fill)
                    .frame(width: span, height: 4)
                    .offset(x: start)
                Capsule()
                    .fill(LinearGradient(colors: stops.map(\.color), startPoint: .leading, endPoint: .trailing))
                    .frame(width: span, height: 4)
                    .mask(alignment: .leading) {
                        Rectangle().frame(width: shown ? span * progress : 0)
                    }
                    .offset(x: start)
                    .animation(.easeOut(duration: 1).delay(0.2), value: shown)
                ForEach(Array(stops.enumerated()), id: \.element.id) { index, stop in
                    let on = stop.count > 0 && shown
                    Circle()
                        .fill(on ? stop.color : WTheme.canvas)
                        .overlay(Circle().strokeBorder(on ? stop.color : WTheme.hairlineStrong, lineWidth: 2))
                        .frame(width: 12, height: 12)
                        .scaleEffect(on ? 1 : 0.85)
                        .offset(x: start + column * CGFloat(index) - 6)
                        .animation(.spring(response: 0.45, dampingFraction: 0.6).delay(0.25 + Double(index) * 0.18), value: shown)
                }
            }
            .frame(height: 12)
        }
        .frame(height: 12)
    }

    @ViewBuilder
    private var caption: some View {
        if let latest = Self.latest(engagement) {
            HStack(spacing: 5) {
                Image(systemName: latest.kind.symbol)
                    .font(.system(size: 11, weight: .semibold))
                Text("\(latest.kind.label) \(WFormat.agoLong(latest.at))")
                    .font(.system(size: 13, weight: .medium))
            }
            .foregroundStyle(latest.kind == .sent ? WTheme.inkSecondary : latest.kind.color)
            .frame(maxWidth: .infinity)
        } else {
            Text("Not emailed yet")
                .font(.system(size: 13))
                .foregroundStyle(WTheme.inkTertiary)
                .frame(maxWidth: .infinity)
        }
    }
}
