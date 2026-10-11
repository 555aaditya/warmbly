import SwiftUI

// Chrome shared by sign-in, the first-run questions and workspace creation:
// one top bar with the step progress, and a sky that never crams.

/// The top bar over the sky: back (or the wordmark on a first step), the
/// step progress centered, and one trailing action.
struct FlowTopBar<Trailing: View>: View {
    var onBack: (() -> Void)?
    var progress: (index: Int, total: Int)?
    @ViewBuilder var trailing: () -> Trailing

    var body: some View {
        ZStack {
            HStack(spacing: 12) {
                if let onBack {
                    FlowSkyButton(symbol: "chevron.left", label: "Back", action: onBack)
                        .transition(.opacity.combined(with: .scale(scale: 0.7)))
                } else {
                    FlowWordmark()
                        .transition(.opacity)
                }
                Spacer(minLength: 12)
                trailing()
            }
            if let progress {
                FlowProgress(index: progress.index, total: progress.total)
                    .transition(.opacity)
            }
        }
        .frame(height: 44)
        .padding(.horizontal, 16)
        .padding(.top, 6)
        .animation(.spring(response: 0.45, dampingFraction: 0.86), value: onBack == nil)
    }
}

/// The logo and name, for the screen a flow opens on.
struct FlowWordmark: View {
    var body: some View {
        HStack(spacing: 8) {
            WarmblyLogo()
                .fill(WTheme.onSky)
                .frame(width: 25, height: 26)
            Text("Warmbly")
                .font(.system(size: 19, weight: .semibold))
                .tracking(-0.5)
                .foregroundStyle(WTheme.onSky)
                .fixedSize()
        }
        .accessibilityElement(children: .combine)
    }
}

/// A round glass button on the sky.
struct FlowSkyButton: View {
    let symbol: String
    let label: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(WTheme.onSky)
                .frame(width: 40, height: 40)
                .background(WTheme.skyTile(0.12), in: Circle())
                .overlay(Circle().strokeBorder(WTheme.skyEdge(0.16), lineWidth: 1))
        }
        .buttonStyle(PressableButtonStyle())
        .accessibilityLabel(label)
    }
}

/// A text pill on the sky, like Sign out.
struct FlowSkyPill: View {
    let title: String
    let action: () -> Void

    var body: some View {
        Button(title, action: action)
            .font(.system(size: 14, weight: .semibold))
            .foregroundStyle(WTheme.onSky)
            .padding(.horizontal, 15)
            .frame(height: 36)
            .background(WTheme.skyTile(0.12), in: Capsule())
            .overlay(Capsule().strokeBorder(WTheme.skyEdge(0.16), lineWidth: 1))
            .buttonStyle(PressableButtonStyle())
    }
}

/// Where a flow is: one segment per step, the current one long.
struct FlowProgress: View {
    let index: Int
    let total: Int

    var body: some View {
        HStack(spacing: 5) {
            ForEach(0 ..< total, id: \.self) { step in
                Capsule()
                    .fill(WTheme.onSky.opacity(step <= index ? 0.88 : 0.18))
                    .frame(width: step == index ? 24 : 12, height: 4)
            }
        }
        .animation(.spring(response: 0.42, dampingFraction: 0.8), value: index)
        .accessibilityElement()
        .accessibilityLabel("Step \(index + 1) of \(total)")
    }
}

/// The sky between the top bar and the sheet. With room it holds the step's
/// icon and name over the flight ambience; when the keyboard takes the room it
/// is just sky, since the top bar already says where you are.
struct FlowSkyStage: View {
    let icon: String
    let title: String
    var onTap: () -> Void = {}

    /// Below this the badge would crowd the top bar, so it steps aside.
    static let roomyHeight: CGFloat = 150

    @State private var appeared = false

    var body: some View {
        GeometryReader { proxy in
            let roomy = proxy.size.height >= Self.roomyHeight
            ZStack {
                Color.clear
                    .contentShape(Rectangle())
                    .onTapGesture(perform: onTap)
                if roomy {
                    HeroFlightScene()
                        .transition(.opacity)
                    badge
                        .scaleEffect(appeared ? 1 : 0.9)
                        .opacity(appeared ? 1 : 0)
                        .transition(.opacity.combined(with: .scale(scale: 0.92)))
                }
            }
            .frame(width: proxy.size.width, height: proxy.size.height)
            .animation(.easeOut(duration: 0.22), value: roomy)
        }
        .onAppear {
            withAnimation(.spring(response: 0.7, dampingFraction: 0.75).delay(0.05)) { appeared = true }
        }
    }

    private var badge: some View {
        VStack(spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 21, weight: .semibold))
                .foregroundStyle(WTheme.onSky)
                .contentTransition(.symbolEffect(.replace))
                .frame(width: 56, height: 56)
                .background(WTheme.skyTile(0.16), in: Circle())
                .overlay(Circle().strokeBorder(WTheme.skyEdge(0.3), lineWidth: 1))
            Text(title)
                .font(.system(size: 16, weight: .semibold))
                .foregroundStyle(WTheme.onSky)
                .contentTransition(.opacity)
        }
        .shadow(color: Color(hex: 0x0C4A6E).opacity(0.18), radius: 8, y: 3)
        .animation(.spring(response: 0.45, dampingFraction: 0.85), value: icon)
    }
}
