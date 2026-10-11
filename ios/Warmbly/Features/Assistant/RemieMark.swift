import SwiftUI

/// Remie's palette, matching the web panel.
enum Remie {
    static let sky500 = Color(hex: 0x0EA5E9)
    static let sky600 = Color(hex: 0x0284C7)
    static let emerald = Color(hex: 0x10B981)
    static let amber = Color.dynamic(light: 0xB45309, dark: 0xFBBF24)
    static let amberTint = Color.dynamic(light: 0xFFFBEB, dark: 0x2A2010)
    static let amberLine = Color.dynamic(light: 0xFDE68A, dark: 0x4A3713)
}

enum RemieMarkState: Equatable {
    case idle, thinking, attention
}

/// What the mark shows: the run state plus the done and muted looks.
enum RemieLook: Equatable {
    case idle, thinking, attention, done, muted

    var colors: (Color, Color, Color) {
        switch self {
        case .idle, .thinking: (Color(hex: 0x7DD3FC), Color(hex: 0x0EA5E9), Color(hex: 0x2563EB))
        case .attention: (Color(hex: 0xFCD34D), Color(hex: 0xF59E0B), Color(hex: 0xEA580C))
        case .done: (Color(hex: 0x6EE7B7), Color(hex: 0x10B981), Color(hex: 0x0D9488))
        case .muted: (
                Color.dynamic(light: 0xE2E8F0, dark: 0x62666D),
                Color.dynamic(light: 0x94A3B8, dark: 0x35373C),
                Color.dynamic(light: 0xCBD5E1, dark: 0x26272B)
            )
        }
    }

    var tint: Color {
        switch self {
        case .idle, .thinking: Color(hex: 0x0EA5E9)
        case .attention: Color(hex: 0xF59E0B)
        case .done: Color(hex: 0x10B981)
        case .muted: Color.dynamic(light: 0x94A3B8, dark: 0x62666D)
        }
    }

    /// Seconds per blob breath and per swirl turn.
    var periods: (body: Double, swirl: Double) {
        switch self {
        case .thinking: (1.6, 1.2)
        case .muted: (9, 7)
        default: (5, 7)
        }
    }
}

/// Integrates phase across frames so a speed change never jumps, and fades
/// colors between looks.
private final class RemieClock {
    var last: Date?
    var time: Double = 0
    var bodyPhase: Double = 0
    var swirlPhase: Double = 0
    var look: RemieLook?
    var previous: RemieLook?
    var changedAt: Double = -10

    func advance(to date: Date, look target: RemieLook) {
        let dt = last.map { min(0.1, max(0, date.timeIntervalSince($0))) } ?? 0
        last = date
        time += dt
        let periods = target.periods
        bodyPhase += dt / periods.body
        swirlPhase += dt / periods.swirl
        if look != target {
            previous = look ?? target
            look = target
            changedAt = time
        }
    }

    func colors() -> (Color, Color, Color) {
        guard let look else { return RemieLook.idle.colors }
        let to = look.colors
        guard let previous, previous != look else { return to }
        let p = min(1, (time - changedAt) / 0.6)
        guard p < 1 else { return to }
        let from = previous.colors
        let e = p * p * (3 - 2 * p)
        return (from.0.mix(with: to.0, by: e), from.1.mix(with: to.1, by: e), from.2.mix(with: to.2, by: e))
    }
}

/// The breathing outline: a circle whose radius ripples with the phase.
private struct BlobShape: Shape {
    var phase: Double

    func path(in rect: CGRect) -> Path {
        let a = phase * 2 * .pi
        let sx = 1 + 0.035 * sin(a)
        let sy = 1 - 0.03 * sin(a)
        let center = CGPoint(x: rect.midX, y: rect.midY)
        let base = min(rect.width, rect.height) / 2 / 1.06
        var path = Path()
        let steps = 72
        for i in 0 ... steps {
            let t = Double(i) / Double(steps) * 2 * .pi
            let r = base * (1 + 0.045 * sin(2 * t + a) + 0.028 * sin(3 * t - a + 1))
            let p = CGPoint(x: center.x + cos(t) * r * sx, y: center.y + sin(t) * r * sy)
            if i == 0 { path.move(to: p) } else { path.addLine(to: p) }
        }
        path.closeSubpath()
        return path
    }
}

private struct Squash: Equatable {
    var x: CGFloat = 1
    var y: CGFloat = 1
    var lift: CGFloat = 0
}

/// Remie's icon: a soft blob with light swirling inside. It breathes at rest,
/// swirls faster while a run works, hops in amber while a run waits on
/// approval, bounces in green when a run finishes, and boops on `boop`.
struct RemieMark: View {
    var size: CGFloat = 20
    var state: RemieMarkState = .idle
    var muted = false
    /// Whether a run ending now succeeded; only then does the mark celebrate.
    var celebrate = true
    /// Bumped on composer keystrokes.
    var boop = 0
    /// Jiggles when poked. Off inside a button, where the tap belongs to the button.
    var tappable = false

    @State private var clock = RemieClock()
    @State private var justFinished = false
    @State private var jelly = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var look: RemieLook {
        if justFinished && state == .idle { return .done }
        if muted && state == .idle { return .muted }
        switch state {
        case .idle: return .idle
        case .thinking: return .thinking
        case .attention: return .attention
        }
    }

    var body: some View {
        TimelineView(.animation(paused: reduceMotion && clock.look == look)) { context in
            let _ = clock.advance(to: context.date, look: look)
            let colors = clock.colors()
            ZStack {
                ZStack {
                    AngularGradient(
                        colors: [colors.0, colors.1, colors.2, colors.0],
                        center: .center,
                        angle: .degrees(reduceMotion ? 0 : clock.swirlPhase * 360)
                    )
                    .frame(width: size * 1.6, height: size * 1.6)
                    .blur(radius: size * 0.07)
                }
                .frame(width: size * 0.88, height: size * 0.88)
                .clipShape(BlobShape(phase: reduceMotion ? 0 : clock.bodyPhase))
                .shadow(color: colors.1.opacity(0.55), radius: size * 0.12, y: size * 0.08)

                Ellipse()
                    .fill(RadialGradient(
                        colors: [.white.opacity(0.9), .white.opacity(0)],
                        center: .center,
                        startRadius: 0,
                        endRadius: size * 0.16
                    ))
                    .frame(width: size * 0.32, height: size * 0.22)
                    .rotationEffect(.degrees(-28))
                    .opacity(look == .muted ? 0.6 : 1)
                    .position(x: size * 0.38, y: size * 0.27)
            }
            .frame(width: size, height: size)
            .offset(y: look == .attention && !reduceMotion ? -hop(clock.time) * size : 0)
        }
        .frame(width: size, height: size)
        .keyframeAnimator(initialValue: Squash(), trigger: jelly) { content, s in
            content.scaleEffect(x: s.x, y: s.y)
        } keyframes: { _ in
            KeyframeTrack(\.x) {
                SpringKeyframe(1.22, duration: 0.18)
                SpringKeyframe(0.9, duration: 0.15)
                SpringKeyframe(1.06, duration: 0.12)
                SpringKeyframe(1, duration: 0.15)
            }
            KeyframeTrack(\.y) {
                SpringKeyframe(0.84, duration: 0.18)
                SpringKeyframe(1.12, duration: 0.15)
                SpringKeyframe(0.96, duration: 0.12)
                SpringKeyframe(1, duration: 0.15)
            }
        }
        .keyframeAnimator(initialValue: Squash(), trigger: boop) { content, s in
            content.scaleEffect(x: s.x, y: s.y)
        } keyframes: { _ in
            KeyframeTrack(\.x) {
                CubicKeyframe(1.16, duration: 0.11)
                CubicKeyframe(1, duration: 0.17)
            }
            KeyframeTrack(\.y) {
                CubicKeyframe(0.88, duration: 0.11)
                CubicKeyframe(1, duration: 0.17)
            }
        }
        .keyframeAnimator(initialValue: Squash(), trigger: justFinished) { content, s in
            content
                .scaleEffect(x: s.x, y: s.y, anchor: .bottom)
                .offset(y: -s.lift * size)
        } keyframes: { _ in
            KeyframeTrack(\.x) {
                SpringKeyframe(1.18, duration: 0.21)
                SpringKeyframe(0.94, duration: 0.18)
                SpringKeyframe(1, duration: 0.31)
            }
            KeyframeTrack(\.y) {
                SpringKeyframe(0.88, duration: 0.21)
                SpringKeyframe(1.1, duration: 0.18)
                SpringKeyframe(1, duration: 0.31)
            }
            KeyframeTrack(\.lift) {
                SpringKeyframe(0, duration: 0.21)
                SpringKeyframe(0.14, duration: 0.18)
                SpringKeyframe(0, duration: 0.31)
            }
        }
        .contentShape(Circle())
        .simultaneousGesture(TapGesture().onEnded { if !reduceMotion { jelly += 1 } }, isEnabled: tappable)
        .onChange(of: state) { old, new in
            guard old == .thinking, new == .idle, celebrate else { return }
            justFinished = true
            Task {
                try? await Task.sleep(for: .milliseconds(1400))
                justFinished = false
            }
        }
        .accessibilityHidden(true)
    }

    /// The amber hop, as a fraction of the mark's size.
    private func hop(_ t: Double) -> CGFloat {
        let f = t.truncatingRemainder(dividingBy: 1.4) / 1.4
        func ease(_ x: Double) -> Double { x * x * (3 - 2 * x) }
        switch f {
        case ..<0.18: return 0.16 * ease(f / 0.18)
        case ..<0.32: return 0.16 * (1 - ease((f - 0.18) / 0.14))
        case ..<0.42: return 0.06 * ease((f - 0.32) / 0.10)
        case ..<0.55: return 0.06 * (1 - ease((f - 0.42) / 0.13))
        default: return 0
        }
    }
}

/// The bare pixel spark, for inline loaders: a light chases around the ring
/// while thinking and the spark pulses while waiting on approval.
struct RemieSpark: View {
    var size: CGFloat = 16
    var state: RemieMarkState = .idle
    var muted = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private static let base: [Double] = [0, 0.62, 0, 0.62, 1, 0.62, 0, 0.62, 0]
    /// The outer ring, clockwise from the top left.
    private static let ring = [0, 1, 2, 5, 8, 7, 6, 3]

    private var look: RemieLook {
        if muted && state == .idle { return .muted }
        switch state {
        case .idle: return .idle
        case .thinking: return .thinking
        case .attention: return .attention
        }
    }

    var body: some View {
        let animated = state != .idle && !reduceMotion
        TimelineView(.animation(paused: !animated)) { context in
            grid(time: context.date.timeIntervalSinceReferenceDate)
        }
        .frame(width: size, height: size)
        .animation(.easeOut(duration: 0.22), value: state)
        .accessibilityHidden(true)
    }

    private func grid(time: Double) -> some View {
        let cell = size * 0.26
        let gap = max(0.5, size * 0.1)
        return VStack(spacing: gap) {
            ForEach(0 ..< 3, id: \.self) { row in
                HStack(spacing: gap) {
                    ForEach(0 ..< 3, id: \.self) { col in
                        let i = row * 3 + col
                        RoundedRectangle(cornerRadius: cell * 0.24, style: .continuous)
                            .fill(look.tint)
                            .frame(width: cell, height: cell)
                            .opacity(opacity(i, time: time))
                    }
                }
            }
        }
    }

    private func opacity(_ i: Int, time: Double) -> Double {
        let o = Self.base[i]
        guard !reduceMotion else { return o }
        switch state {
        case .thinking:
            guard let r = Self.ring.firstIndex(of: i) else { return 1 }
            var f = (time - Double(r) * 0.11).truncatingRemainder(dividingBy: 0.88) / 0.88
            if f < 0 { f += 1 }
            if f < 0.12 { return 0.2 + 0.8 * (f / 0.12) }
            if f < 0.45 { return 1 - 0.8 * ((f - 0.12) / 0.33) }
            return 0.2
        case .attention:
            guard o > 0 else { return 0 }
            let wave = 0.5 - 0.5 * cos(2 * .pi * time / 1.2)
            return o * (1 - 0.65 * wave)
        case .idle:
            return o
        }
    }
}

/// Live status text: a sky light sweeping across slate, like the web's
/// `ai-shimmer-text`.
struct RemieShimmer: ViewModifier {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func body(content: Content) -> some View {
        if reduceMotion {
            content.foregroundStyle(WTheme.inkSecondary)
        } else {
            content
                .foregroundStyle(WTheme.inkSecondary)
                .overlay {
                    TimelineView(.animation) { context in
                        let t = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 1.8) / 1.8
                        GeometryReader { proxy in
                            let w = proxy.size.width
                            LinearGradient(
                                colors: [Remie.sky500.opacity(0), Remie.sky500, Remie.sky500.opacity(0)],
                                startPoint: .leading,
                                endPoint: .trailing
                            )
                            .frame(width: max(24, w * 0.45))
                            .offset(x: -max(24, w * 0.45) + (w + max(24, w * 0.45)) * t)
                        }
                    }
                    .mask(content)
                    .allowsHitTesting(false)
                }
        }
    }
}

extension View {
    func remieShimmer() -> some View { modifier(RemieShimmer()) }
}
