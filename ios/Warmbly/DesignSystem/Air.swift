import SwiftUI

// MARK: - Tab switching from embedded screens

/// Lets Home's cards jump straight to a tab (campaigns, inbox) without
/// plumbing bindings through every layer.
struct SwitchTabKey: EnvironmentKey {
    static let defaultValue: @MainActor @Sendable (AppTab) -> Void = { _ in }
}

extension EnvironmentValues {
    var switchTab: @MainActor @Sendable (AppTab) -> Void {
        get { self[SwitchTabKey.self] }
        set { self[SwitchTabKey.self] = newValue }
    }
}

// MARK: - Cards

/// The in-app surface: a soft filled panel, no outline and no shadow.
struct AirCardModifier: ViewModifier {
    var padding: CGFloat = 16

    func body(content: Content) -> some View {
        content
            .padding(padding)
            .background(WTheme.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
    }
}

extension View {
    func airCard(padding: CGFloat = 16) -> some View {
        modifier(AirCardModifier(padding: padding))
    }
}

/// Section heading above a card: bold title, optional trailing action.
struct AirSectionHeader: View {
    let title: String
    var actionTitle: String?
    var action: (() -> Void)?

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(title)
                .displayFont(20)
                .foregroundStyle(WTheme.ink)
            Spacer()
            if let actionTitle, let action {
                Button(action: action) {
                    HStack(spacing: 4) {
                        Text(actionTitle)
                        Image(systemName: "arrow.right")
                            .font(.system(size: 11, weight: .medium))
                    }
                    .font(.system(size: 13.5, weight: .medium))
                    .foregroundStyle(WTheme.inkSecondary)
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 2)
    }
}

// MARK: - Icon tile

/// Rounded-square tinted symbol tile that leads list rows and quick links.
struct IconTile: View {
    let symbol: String
    var tone: Tone = .sky
    var size: CGFloat = 38

    var body: some View {
        Image(systemName: symbol)
            .font(.system(size: size * 0.42, weight: .medium))
            .foregroundStyle(tone.color)
            .frame(width: size, height: size)
            .background(tone.background, in: RoundedRectangle(cornerRadius: size * 0.3, style: .continuous))
    }
}

// MARK: - Health ring

/// Small circular score gauge (mailbox health), animated on appearance.
struct HealthRing: View {
    let score: Int?
    var tone: Tone = .emerald
    var size: CGFloat = 34

    @State private var shown = false

    private var fraction: Double {
        guard let score else { return 0 }
        return min(1, max(0.04, Double(score) / 100))
    }

    var body: some View {
        ZStack {
            Circle()
                .stroke(tone.color.opacity(0.18), lineWidth: 3.5)
            Circle()
                .trim(from: 0, to: shown ? fraction : 0)
                .stroke(tone.color, style: StrokeStyle(lineWidth: 3.5, lineCap: .round))
                .rotationEffect(.degrees(-90))
            if let score {
                Text("\(score)")
                    .font(.system(size: size * 0.34, weight: .semibold))
                    .monospacedDigit()
                    .foregroundStyle(tone.color)
            } else {
                Image(systemName: "minus")
                    .font(.system(size: size * 0.3, weight: .semibold))
                    .foregroundStyle(.tertiary)
            }
        }
        .frame(width: size, height: size)
        .onAppear {
            withAnimation(.spring(response: 0.9, dampingFraction: 0.85).delay(0.15)) {
                shown = true
            }
        }
    }
}

// MARK: - Hero stat

/// An open stat for screen headers: a tinted glyph and label over a big
/// number, no tile around it.
struct AirStatChip: View {
    let value: String
    let label: String
    var symbol: String?
    var tint: Color = WTheme.accent

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 5) {
                if let symbol {
                    Image(systemName: symbol)
                        .font(.system(size: 10.5, weight: .semibold))
                        .foregroundStyle(tint)
                } else {
                    Circle().fill(tint).frame(width: 6, height: 6)
                }
                Text(label)
                    .font(.system(size: 12.5, weight: .medium))
                    .foregroundStyle(WTheme.inkSecondary)
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)
            }
            Text(value)
                .font(.system(size: 26, weight: .semibold, design: .rounded))
                .tracking(-0.6)
                .monospacedDigit()
                .foregroundStyle(WTheme.ink)
                .contentTransition(.numericText())
                .lineLimit(1)
                .minimumScaleFactor(0.7)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

// MARK: - Detail scaffold (sky hero + sticky pill tabs)

/// One tab in an AirPillTabBar.
struct AirTabItem: Identifiable, Hashable {
    let id: String
    let title: String
    let icon: String
}

/// Flat full-width tab bar in the dashboard's underline language: every tab
/// gets an equal share (no horizontal scrolling or cut-off tabs), the active
/// one is bold with an accent underline that slides between tabs.
struct AirUnderlineTabBar: View {
    let tabs: [AirTabItem]
    @Binding var selection: String
    @Namespace private var underlineNS

    var body: some View {
        HStack(spacing: 0) {
            ForEach(tabs) { tab in
                tabButton(tab)
            }
        }
        .padding(.horizontal, 8)
        .overlay(alignment: .bottom) { Rectangle().fill(WTheme.hairline).frame(height: 1) }
        .sensoryFeedback(.selection, trigger: selection)
    }

    private func tabButton(_ tab: AirTabItem) -> some View {
        let selected = tab.id == selection
        return Button {
            withAnimation(.spring(response: 0.35, dampingFraction: 0.82)) {
                selection = tab.id
            }
        } label: {
            VStack(spacing: 0) {
                HStack(spacing: 5) {
                    Image(systemName: tab.icon)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(selected ? WTheme.ink : WTheme.inkTertiary)
                    Text(tab.title)
                        .font(.system(size: 13.5, weight: selected ? .medium : .regular))
                        .foregroundStyle(selected ? WTheme.ink : WTheme.inkSecondary)
                        .lineLimit(1)
                        .minimumScaleFactor(0.8)
                }
                .frame(maxWidth: .infinity)
                .frame(height: 46)
                ZStack {
                    if selected {
                        Capsule()
                            .fill(WTheme.ink)
                            .frame(height: 2)
                            .matchedGeometryEffect(id: "air-underline", in: underlineNS)
                    } else {
                        Color.clear.frame(height: 2)
                    }
                }
                .padding(.horizontal, 10)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

/// Detail-screen scaffold: a clean header on the canvas, then a pinned
/// underline tab bar with the tab content swiping underneath. Tab contents
/// keep their own Lists, scrolling, and swipe actions.
struct AirDetailScaffold<Hero: View, Content: View>: View {
    let tabs: [AirTabItem]
    @Binding var selection: String
    @ViewBuilder var hero: Hero
    @ViewBuilder var content: Content

    var body: some View {
        VStack(spacing: 0) {
            // The hero keeps its full height; the paged content takes the rest.
            hero
                .frame(maxWidth: .infinity, alignment: .leading)
                .fixedSize(horizontal: false, vertical: true)
            AirUnderlineTabBar(tabs: tabs, selection: $selection)
            content
        }
        .background(WTheme.canvas)
        .toolbarBackground(WTheme.canvas, for: .navigationBar)
    }
}

/// Header backdrop for heros and drawers: the canvas with a faint sky haze
/// and dot grid along the top edge, so a header reads as a place without
/// a slab of colour behind it.
struct AirSkyWash: View {
    var body: some View {
        ZStack(alignment: .top) {
            WTheme.canvas
            DotGrid(spacing: 18, opacity: 0.05)
                .mask(LinearGradient(colors: [.black, .clear], startPoint: .top, endPoint: .bottom))
            RadialGradient(
                colors: [WTheme.accent.opacity(0.09), .clear],
                center: UnitPoint(x: 0.85, y: 0),
                startRadius: 0,
                endRadius: 320
            )
        }
    }
}

// MARK: - Site primitives

/// The site's dot-grid texture (`bg-dotgrid`), in the current ink.
struct DotGrid: View {
    var spacing: CGFloat = 22
    var opacity: Double = 0.08
    var color: Color = WTheme.ink

    var body: some View {
        Canvas { ctx, size in
            var path = Path()
            var y: CGFloat = spacing / 2
            while y < size.height {
                var x: CGFloat = spacing / 2
                while x < size.width {
                    path.addEllipse(in: CGRect(x: x - 0.75, y: y - 0.75, width: 1.5, height: 1.5))
                    x += spacing
                }
                y += spacing
            }
            ctx.fill(path, with: .color(color.opacity(opacity)))
        }
        .allowsHitTesting(false)
    }
}

/// A full-width hairline rule.
struct Hairline: View {
    var body: some View {
        Rectangle().fill(WTheme.hairline).frame(height: 1)
    }
}

/// The site's two-tone heading: a grey lead, then the point in ink.
struct TwoToneTitle: View {
    var lead: String?
    let title: String
    var size: CGFloat = 30
    var leadColor: Color = WTheme.inkTertiary
    var titleColor: Color = WTheme.ink

    var body: some View {
        Group {
            if let lead {
                Text("\(Text(lead).foregroundStyle(leadColor)) \(Text(title).foregroundStyle(titleColor))")
            } else {
                Text(title).foregroundStyle(titleColor)
            }
        }
        .displayFont(size)
        .fixedSize(horizontal: false, vertical: true)
    }
}

/// A live/state dot with the web's ping halo.
struct LiveDot: View {
    var color: Color = WTheme.positive
    var size: CGFloat = 7
    var pulsing = true

    var body: some View {
        Circle()
            .fill(color)
            .frame(width: size, height: size)
            .modifier(PingEffect(active: pulsing, color: color))
    }
}

/// One of the site's photo frames: a soft texture with UI floating on it.
struct PhotoFrame<Content: View>: View {
    var photo = "SheerSky"
    var cornerRadius: CGFloat = 22
    @ViewBuilder var content: Content
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        content
            .background {
                Image(photo)
                    .resizable()
                    .scaledToFill()
                    .overlay(WTheme.night.opacity(colorScheme == .dark ? 0.45 : 0))
                    .allowsHitTesting(false)
            }
            .clipShape(RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                    .strokeBorder(WTheme.ink.opacity(0.06), lineWidth: 1)
            )
    }
}

// MARK: - Buttons

enum ButtonSize {
    case small, regular, large

    var height: CGFloat {
        switch self {
        case .small: 34
        case .regular: 44
        case .large: 54
        }
    }

    var font: Font {
        switch self {
        case .small: .system(size: 13.5, weight: .medium)
        case .regular: .system(size: 15, weight: .medium)
        case .large: .system(size: 16.5, weight: .medium)
        }
    }

    var padding: CGFloat {
        switch self {
        case .small: 14
        case .regular: 18
        case .large: 22
        }
    }
}

/// The site's primary action: a near-black pill (white in dark mode).
struct InkButtonStyle: ButtonStyle {
    var size: ButtonSize = .regular
    var fullWidth = false
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(size.font)
            .foregroundStyle(WTheme.inkInverse)
            .padding(.horizontal, size.padding)
            .frame(maxWidth: fullWidth ? .infinity : nil)
            .frame(height: size.height)
            .background(WTheme.ink.opacity(isEnabled ? 1 : 0.22), in: Capsule())
            .scaleEffect(configuration.isPressed ? 0.97 : 1)
            .opacity(configuration.isPressed ? 0.86 : 1)
            .animation(.spring(response: 0.28, dampingFraction: 0.75), value: configuration.isPressed)
            .animation(.easeOut(duration: 0.18), value: isEnabled)
    }
}

/// The site's secondary action: a hairline pill on the canvas.
struct OutlineButtonStyle: ButtonStyle {
    var size: ButtonSize = .regular
    var fullWidth = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(size.font)
            .foregroundStyle(WTheme.ink)
            .padding(.horizontal, size.padding)
            .frame(maxWidth: fullWidth ? .infinity : nil)
            .frame(height: size.height)
            .background(configuration.isPressed ? WTheme.fill : WTheme.raised, in: Capsule())
            .overlay(Capsule().strokeBorder(WTheme.hairlineStrong, lineWidth: 1))
            .scaleEffect(configuration.isPressed ? 0.97 : 1)
            .animation(.spring(response: 0.28, dampingFraction: 0.75), value: configuration.isPressed)
    }
}

// MARK: - Web handoff

/// Points at the web dashboard for work that needs a big screen (sequence
/// editing, sender assignment, advanced tuning). Informational, not a link:
/// there is deliberately no in-app browser for editing flows.
struct WebHandoffBanner: View {
    let text: String

    var body: some View {
        HStack(alignment: .top, spacing: 11) {
            Image(systemName: "macbook")
                .font(.system(size: 14, weight: .regular))
                .foregroundStyle(WTheme.ink)
                .frame(width: 30, height: 30)
                .background(WTheme.fill, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
            VStack(alignment: .leading, spacing: 2) {
                Text("Best on a big screen")
                    .font(.footnote.weight(.semibold))
                Text(text)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(WTheme.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .strokeBorder(style: StrokeStyle(lineWidth: 1, dash: [3, 3]))
                .foregroundStyle(WTheme.hairlineStrong)
        )
    }
}

// MARK: - Press feedback

/// Gentle press-down scale for tappable cards and rows.
struct TapScaleStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .opacity(configuration.isPressed ? 0.92 : 1)
            .animation(.spring(response: 0.28, dampingFraction: 0.8), value: configuration.isPressed)
    }
}
