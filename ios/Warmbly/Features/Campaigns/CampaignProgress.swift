import SwiftUI

/// How far a campaign is through its leads: a bar in the status tone that
/// fills in on arrival, with a light running along it while it sends.
struct CampaignProgressBar: View {
    var progress: Double
    var tone: Color
    var live = false
    var height: CGFloat = 6
    var delay: Double = 0

    @State private var filled: Double = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        GeometryReader { proxy in
            let width = proxy.size.width * filled
            ZStack(alignment: .leading) {
                Capsule().fill(WTheme.fill)
                Capsule()
                    .fill(LinearGradient(colors: [tone.opacity(0.7), tone], startPoint: .leading, endPoint: .trailing))
                    .frame(width: max(filled > 0 ? height : 0, width))
                    .overlay {
                        if live, !reduceMotion, width > 0 {
                            ProgressGlint(width: width)
                                .clipShape(Capsule())
                        }
                    }
            }
        }
        .frame(height: height)
        .onAppear { fill(delay: 0.15 + delay) }
        .onChange(of: progress) { fill(delay: 0) }
        .accessibilityElement()
        .accessibilityLabel("Campaign progress")
        .accessibilityValue("\(Int(progress * 100)) percent")
    }

    private func fill(delay: Double) {
        if reduceMotion { filled = progress; return }
        withAnimation(.spring(response: 1.1, dampingFraction: 0.86).delay(delay)) { filled = progress }
    }
}

/// A soft light that passes along a filled bar every few seconds.
struct ProgressGlint: View {
    let width: CGFloat

    var body: some View {
        TimelineView(.animation) { context in
            let t = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 3.2) / 3.2
            LinearGradient(colors: [.white.opacity(0), .white.opacity(0.5), .white.opacity(0)], startPoint: .leading, endPoint: .trailing)
                .frame(width: 44)
                .offset(x: -44 + (width + 88) * min(1, t / 0.55))
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .allowsHitTesting(false)
    }
}

/// "72% done" that counts up from zero the first time it is drawn.
struct CampaignPercentText: View {
    let progress: Double
    var size: CGFloat = 14
    var delay: Double = 0

    @State private var shown: Double = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        CountUpText(value: shown * 100) { value in
            let percent = Int(value.rounded(.down))
            return percent >= 100 ? "Done" : "\(percent)% done"
        }
        .font(.system(size: size, weight: .semibold))
        .monospacedDigit()
        .foregroundStyle(WTheme.ink)
        .onAppear { count(delay: 0.15 + delay) }
        .onChange(of: progress) { count(delay: 0) }
    }

    private func count(delay: Double) {
        if reduceMotion { shown = progress; return }
        withAnimation(.easeOut(duration: 1.1).delay(delay)) { shown = progress }
    }
}

/// The campaign page's progress: a large percentage, then every lead split by
/// how its flow ended, each part growing in one after another.
struct CampaignProgressHero: View {
    let counts: CampaignLeadCounts
    var live = false

    @State private var grown = false
    @State private var percent: Double = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private struct Part: Identifiable {
        let id: String
        let label: String
        let count: Int
        let color: Color
    }

    private var total: Int { max(counts.total ?? 0, 1) }

    private var parts: [Part] {
        let toGo = max(0, (counts.total ?? 0) - counts.finished)
        return [
            Part(id: "replied", label: "replied", count: counts.replied ?? 0, color: WTheme.positive),
            Part(id: "completed", label: "completed", count: counts.completed ?? 0, color: WTheme.accent),
            Part(id: "bounced", label: "bounced", count: (counts.bounced ?? 0) + (counts.failed ?? 0) + (counts.undeliverable ?? 0), color: WTheme.negative),
            Part(id: "unsubscribed", label: "unsubscribed", count: counts.unsubscribed ?? 0, color: WTheme.inkTertiary),
            Part(id: "togo", label: "to go", count: toGo, color: WTheme.fill),
        ]
        .filter { $0.count > 0 }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .lastTextBaseline, spacing: 8) {
                CountUpText(value: percent * 100) { "\(Int($0.rounded(.down)))%" }
                    .font(.system(size: 40, weight: .semibold, design: .rounded))
                    .monospacedDigit()
                    .foregroundStyle(WTheme.ink)
                Text((counts.progress ?? 0) >= 1 ? "done" : "through its leads")
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(WTheme.inkSecondary)
                Spacer(minLength: 6)
                Text("\(WFormat.compact(counts.finished)) of \(WFormat.compact(counts.total ?? 0))")
                    .font(.system(size: 14))
                    .monospacedDigit()
                    .foregroundStyle(WTheme.inkTertiary)
            }
            bar
            legend
        }
        .onAppear { grow() }
        .onChange(of: counts.finished) { grow() }
        .accessibilityElement(children: .combine)
    }

    private var bar: some View {
        GeometryReader { proxy in
            let gaps = CGFloat(max(0, parts.count - 1)) * 3
            let usable = max(0, proxy.size.width - gaps)
            HStack(spacing: 3) {
                ForEach(Array(parts.enumerated()), id: \.element.id) { index, part in
                    let width = usable * CGFloat(part.count) / CGFloat(total)
                    RoundedRectangle(cornerRadius: 4, style: .continuous)
                        .fill(part.color)
                        .frame(width: grown ? max(4, width) : 4)
                        .overlay {
                            if live, part.id != "togo", !reduceMotion, grown {
                                ProgressGlint(width: width)
                                    .clipShape(RoundedRectangle(cornerRadius: 4, style: .continuous))
                            }
                        }
                        .opacity(grown ? 1 : 0)
                        .animation(.spring(response: 0.9, dampingFraction: 0.86).delay(0.2 + Double(index) * 0.12), value: grown)
                }
            }
        }
        .frame(height: 10)
    }

    private var legend: some View {
        RecipientFlow(spacing: 10) {
            ForEach(parts) { part in
                HStack(spacing: 6) {
                    Circle()
                        .fill(part.id == "togo" ? WTheme.inkTertiary.opacity(0.35) : part.color)
                        .frame(width: 7, height: 7)
                    Text("\(Text(WFormat.compact(part.count)).foregroundStyle(WTheme.ink).fontWeight(.semibold)) \(part.label)")
                        .font(.system(size: 13))
                        .monospacedDigit()
                        .foregroundStyle(WTheme.inkSecondary)
                }
            }
        }
    }

    private func grow() {
        if reduceMotion {
            grown = true
            percent = counts.progress ?? 0
            return
        }
        grown = true
        withAnimation(.easeOut(duration: 1.2).delay(0.2)) { percent = counts.progress ?? 0 }
    }
}
