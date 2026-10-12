import Charts
import SwiftUI

// MARK: - Warmup

/// One warming inbox: its ramp toward the target volume, today's sends, and
/// when it will be ready. The bar fills on arrival and a light runs along it.
struct HomeWarmupRow: View {
    let account: EmailAccount
    let status: AccountAnalytics?
    var shown: Bool
    var delay: Double = 0

    /// Where the ramp stands: today's daily target, else the ramp from its start.
    private var current: Int {
        if let today = status?.warmupStatus?.targetVolume, today > 0 { return min(today, target) }
        let days = status?.warmupStatus?.daysActive ?? 0
        return min(target, (account.warmupBase ?? 10) + (account.warmupIncrease ?? 1) * days)
    }

    /// The ceiling the ramp climbs to.
    private var target: Int {
        max(1, status?.warmupStatus?.maxVolume ?? account.warmupMax ?? 40)
    }

    private var progress: Double { min(1, Double(current) / Double(target)) }

    private var daysToReady: Int? {
        let step = max(1, account.warmupIncrease ?? 1)
        let remaining = target - current
        return remaining > 0 ? Int((Double(remaining) / Double(step)).rounded(.up)) : nil
    }

    private var sentToday: Int? { status?.warmupStatus?.currentVolume ?? status?.dailyUsage?.warmupSent }

    private var paused: Bool { status?.warmupStatus?.paused == true || account.isWarmupPaused }

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            WAvatar(name: account.email, seed: account.id, size: 36)
            VStack(alignment: .leading, spacing: 8) {
                HStack(alignment: .firstTextBaseline) {
                    Text(account.email)
                        .font(.system(size: 15, weight: .medium))
                        .foregroundStyle(WTheme.ink)
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    if let days = status?.warmupStatus?.daysActive {
                        Text("Day \(days)")
                            .font(.system(size: 13, weight: .semibold))
                            .monospacedDigit()
                            .foregroundStyle(paused ? WTheme.inkTertiary : Tone.orange.color)
                    }
                }
                WarmupBar(progress: shown ? progress : 0, paused: paused, delay: delay)
                    .frame(height: 8)
                HStack(spacing: 0) {
                    Text("\(Text("\(current)").foregroundStyle(WTheme.ink).fontWeight(.semibold)) of \(target) a day")
                    if let sentToday {
                        Text("  ·  \(Text("\(sentToday)").foregroundStyle(WTheme.ink).fontWeight(.semibold)) sent today")
                    }
                    Spacer(minLength: 6)
                    Text(paused ? "Paused" : daysToReady.map { "Ready in \($0)d" } ?? "Fully warm")
                        .foregroundStyle(paused ? WTheme.inkTertiary : (daysToReady == nil ? WTheme.positive : WTheme.inkSecondary))
                }
                .font(.system(size: 12.5))
                .foregroundStyle(WTheme.inkSecondary)
                .lineLimit(1)
            }
        }
        .padding(.vertical, 12)
        .contentShape(Rectangle())
    }
}

/// The ramp bar: an ember gradient that grows in, with a soft light passing
/// along the filled part every few seconds while the inbox is warming.
struct WarmupBar: View {
    var progress: Double
    var paused = false
    var delay: Double = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        GeometryReader { proxy in
            let width = proxy.size.width * progress
            ZStack(alignment: .leading) {
                Capsule().fill(WTheme.fill)
                Capsule()
                    .fill(paused
                        ? AnyShapeStyle(WTheme.inkTertiary.opacity(0.5))
                        : AnyShapeStyle(LinearGradient(
                            colors: [Color(hex: 0xFDBA74), Color(hex: 0xF97316), Color(hex: 0xEF4444)],
                            startPoint: .leading,
                            endPoint: .trailing
                        )))
                    .frame(width: max(progress > 0 ? 8 : 0, width))
                    .overlay {
                        if !paused, !reduceMotion, width > 0 {
                            TimelineView(.animation) { context in
                                let t = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 3.2) / 3.2
                                LinearGradient(colors: [.white.opacity(0), .white.opacity(0.55), .white.opacity(0)], startPoint: .leading, endPoint: .trailing)
                                    .frame(width: 44)
                                    .offset(x: -44 + (width + 88) * min(1, t / 0.55))
                            }
                            .clipShape(Capsule())
                        }
                    }
                    .animation(.spring(response: 1.1, dampingFraction: 0.85).delay(0.15 + delay), value: progress)
            }
        }
        .accessibilityElement()
        .accessibilityLabel("Warmup progress")
        .accessibilityValue("\(Int(progress * 100)) percent")
    }
}

// MARK: - Insight pills

/// One short fact on the hero, tappable through to where it is handled.
struct HomeInsightPill: View {
    let symbol: String
    let text: Text
    var tint: Color = WTheme.ink
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 7) {
                Image(systemName: symbol)
                    .font(.system(size: 12.5, weight: .semibold))
                    .foregroundStyle(tint)
                text
                    .font(.system(size: 14, weight: .medium))
                    .foregroundStyle(WTheme.ink)
            }
            .padding(.horizontal, 13)
            .frame(height: 36)
            .contentShape(Capsule())
        }
        .buttonStyle(TapScaleStyle())
        .glassEffect(.regular.interactive(), in: Capsule())
    }
}
