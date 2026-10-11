import SwiftUI

// Small CRM-scoped presentation helpers shared across the module's screens.

// MARK: - Colors and formatting

enum CRMHex {
    /// Parses a `#rrggbb` string (stage/task-type colors) into a Color.
    static func color(_ hex: String?) -> Color? {
        guard var raw = hex?.trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty else { return nil }
        if raw.hasPrefix("#") { raw.removeFirst() }
        guard raw.count == 6, let value = UInt32(raw, radix: 16) else { return nil }
        return Color(hex: value)
    }
}

enum CRMFormat {
    private static let currencySymbols: [String: String] = [
        "USD": "$", "EUR": "€", "GBP": "£", "JPY": "¥", "INR": "₹", "BRL": "R$",
        "AUD": "A$", "CAD": "C$", "NZD": "NZ$", "HUF": "Ft ", "CHF": "CHF ",
        "SEK": "kr ", "NOK": "kr ", "DKK": "kr ", "PLN": "zł ", "CZK": "Kč ",
    ]

    static func symbol(for code: String) -> String {
        currencySymbols[code.uppercased()] ?? "\(code.uppercased()) "
    }

    /// Full currency amount, e.g. "$1,250" / "$1,250.50".
    static func currency(_ value: Double, code: String?) -> String {
        let resolved = (code?.isEmpty == false) ? code!.uppercased() : "USD"
        let wholeNumber = value.truncatingRemainder(dividingBy: 1) == 0
        return value.formatted(
            .currency(code: resolved).precision(.fractionLength(wholeNumber ? 0 : 2))
        )
    }

    /// Compact currency for stat strips, e.g. "$12.3k".
    static func compactCurrency(_ value: Double, code: String?) -> String {
        let resolved = (code?.isEmpty == false) ? code!.uppercased() : "USD"
        if abs(value) >= 1000 {
            return symbol(for: resolved) + WFormat.compact(Int(value))
        }
        return currency(value, code: resolved)
    }

    /// Short due-date label: "today 14:30", "tomorrow", "Jun 12".
    static func dueLabel(_ date: Date) -> String {
        let calendar = Calendar.current
        if calendar.isDateInToday(date) {
            return "today " + date.formatted(date: .omitted, time: .shortened)
        }
        if calendar.isDateInTomorrow(date) { return "tomorrow" }
        if calendar.isDateInYesterday(date) { return "yesterday" }
        return date.formatted(.dateTime.day().month(.abbreviated))
    }

    /// Meeting time label with the time always included.
    static func meetingTime(_ date: Date) -> String {
        let calendar = Calendar.current
        if calendar.isDateInToday(date) {
            return "today " + date.formatted(date: .omitted, time: .shortened)
        }
        if calendar.isDateInTomorrow(date) {
            return "tomorrow " + date.formatted(date: .omitted, time: .shortened)
        }
        return date.formatted(.dateTime.day().month(.abbreviated).hour().minute())
    }
}

// MARK: - Tone mappings

extension CRMDeal {
    var statusTone: Tone {
        switch status {
        case "won": .emerald
        case "lost": .rose
        default: .sky
        }
    }
}

extension CRMTask {
    var priorityTone: Tone {
        switch priority {
        case "urgent": .rose
        case "high": .orange
        case "low": .slate
        default: .sky
        }
    }
}

extension CRMMeeting {
    var statusTone: Tone {
        switch status {
        case "booked": .emerald
        case "rescheduled": .amber
        case "canceled": .rose
        case "no_show": .rose
        case "completed": .slate
        default: .slate
        }
    }

    var statusLabel: String {
        switch status {
        case "no_show": "no show"
        default: status ?? "booked"
        }
    }
}

// MARK: - Browser shell

/// Shared constants for the CRM full-screen browsers.
enum CRMBrowser {
    static let sidebarWidth: CGFloat = 300
    static let spring = Animation.spring(response: 0.34, dampingFraction: 0.86)
}

/// Full-screen drawer-browser scaffold copying the campaign leads browser: the
/// main pane scales back (anchor trailing) when the drawer opens, a dim layer
/// closes on tap, the drawer slides from the leading edge with drag-to-close,
/// and an edge swipe from x < 44 opens it. Presented inside the feature's own
/// NavigationStack with the system bar hidden.
struct CRMBrowserShell<Main: View, Drawer: View>: View {
    @Binding var sidebarOpen: Bool
    let main: () -> Main
    /// Built with the top safe-area inset so the header can run under it.
    let drawer: (CGFloat) -> Drawer

    @State private var sidebarDrag: CGFloat = 0

    init(
        sidebarOpen: Binding<Bool>,
        @ViewBuilder main: @escaping () -> Main,
        @ViewBuilder drawer: @escaping (CGFloat) -> Drawer
    ) {
        _sidebarOpen = sidebarOpen
        self.main = main
        self.drawer = drawer
    }

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                main()
                    .scaleEffect(sidebarOpen ? 0.97 : 1, anchor: .trailing)
                    .simultaneousGesture(
                        DragGesture(minimumDistance: 25)
                            .onEnded { value in
                                if !sidebarOpen, value.startLocation.x < 44, value.translation.width > 70 {
                                    withAnimation(CRMBrowser.spring) { sidebarOpen = true }
                                }
                            }
                    )
                if sidebarOpen {
                    Color.black.opacity(0.22)
                        .ignoresSafeArea()
                        .transition(.opacity)
                        .onTapGesture { close() }
                }
                drawer(geo.safeAreaInsets.top)
                    .frame(width: CRMBrowser.sidebarWidth)
                    .frame(maxHeight: .infinity)
                    .background(WTheme.canvas)
                    .clipShape(UnevenRoundedRectangle(bottomTrailingRadius: 22, topTrailingRadius: 22, style: .continuous))
                    .overlay {
                        UnevenRoundedRectangle(bottomTrailingRadius: 22, topTrailingRadius: 22, style: .continuous)
                            .strokeBorder(WTheme.hairlineStrong, lineWidth: 1)
                            .opacity(sidebarOpen ? 1 : 0)
                    }
                    .ignoresSafeArea()
                    .offset(x: (sidebarOpen ? 0 : -CRMBrowser.sidebarWidth - 40) + sidebarDrag)
                    .gesture(
                        DragGesture()
                            .onChanged { value in sidebarDrag = min(0, value.translation.width) }
                            .onEnded { value in
                                if value.translation.width < -80 || value.predictedEndTranslation.width < -160 {
                                    close()
                                } else {
                                    withAnimation(.spring(response: 0.32, dampingFraction: 0.86)) { sidebarDrag = 0 }
                                }
                            }
                    )
            }
        }
        .sensoryFeedback(.impact(weight: .light), trigger: sidebarOpen)
        #if DEBUG
        // Scripted debug runs can open the drawer (WARMBLY_DEBUG_DRAWER=1).
        .onAppear {
            if ProcessInfo.processInfo.environment["WARMBLY_DEBUG_DRAWER"] != nil { sidebarOpen = true }
        }
        #endif
    }

    private func close() {
        withAnimation(CRMBrowser.spring) {
            sidebarOpen = false
            sidebarDrag = 0
        }
    }
}

// MARK: - Drawer chrome

/// Drawer layout shared by the CRM browsers, mirroring the contacts sidebar:
/// a title, optional subtitle and soft count chips over the scope rows.
struct CRMDrawer<Badges: View, Rows: View>: View {
    let title: String
    var subtitle: String? = nil
    let topInset: CGFloat
    @ViewBuilder let badges: () -> Badges
    @ViewBuilder let rows: () -> Rows

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            hero
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    rows()
                }
                .padding(.horizontal, 12)
                .padding(.top, 4)
                .padding(.bottom, 40)
            }
        }
        .background(WTheme.canvas)
    }

    private var hero: some View {
        VStack(alignment: .leading, spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .displayFont(24, weight: .semibold)
                    .foregroundStyle(WTheme.ink)
                if let subtitle, !subtitle.isEmpty {
                    Text(subtitle)
                        .font(.system(size: 14))
                        .foregroundStyle(WTheme.inkSecondary)
                        .lineLimit(1)
                }
            }
            HStack(spacing: 6) {
                badges()
            }
        }
        .padding(.horizontal, 20)
        .padding(.top, topInset + 18)
        .padding(.bottom, 6)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// Hairline count chip on the drawer header; `live` swaps the symbol for a
/// pulsing state dot.
struct CRMDrawerBadge: View {
    let symbol: String
    let text: String
    var live = false
    var pingColor: Color = WTheme.accent

    var body: some View {
        HStack(spacing: 5) {
            if live {
                LiveDot(color: pingColor, size: 5.5)
                    .frame(width: 10)
            } else {
                Image(systemName: symbol)
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(WTheme.inkSecondary)
            }
            Text(text)
                .font(.system(size: 13, weight: .medium))
                .monospacedDigit()
                .foregroundStyle(WTheme.inkSecondary)
                .contentTransition(.numericText())
                .lineLimit(1)
        }
        .padding(.horizontal, 10)
        .frame(height: 28)
        .background(WTheme.fill, in: Capsule())
    }
}

/// Eyebrow section label between drawer row groups.
struct CRMDrawerSectionLabel: View {
    let text: String

    init(_ text: String) { self.text = text }

    var body: some View {
        EyebrowLabel(text)
            .padding(.horizontal, 14)
            .padding(.top, 14)
            .padding(.bottom, 6)
    }
}

/// Scope row in a CRM drawer: leading icon / dot / pulsing dot, title,
/// trailing mono count, and the sliding fill via matchedGeometryEffect.
/// Rows cascade in with a capped stagger when the drawer reveals.
struct CRMDrawerRow: View {
    var icon: String? = nil
    var dot: Color? = nil
    var pingDot: Color? = nil
    let title: String
    var count: Int? = nil
    let selected: Bool
    let index: Int
    let revealed: Bool
    let namespace: Namespace.ID
    /// Matched-geometry id; use one id per independent selection group.
    var matchedID = "crmdrawer-active"
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 13) {
                Group {
                    if let pingDot {
                        LiveDot(color: pingDot, size: 8)
                    } else if let dot {
                        Circle()
                            .fill(dot)
                            .frame(width: 9, height: 9)
                    } else if let icon {
                        Image(systemName: icon)
                            .font(.system(size: 14.5, weight: .regular))
                            .foregroundStyle(selected ? WTheme.ink : WTheme.inkSecondary)
                    }
                }
                .frame(width: 24)
                Text(title)
                    .font(.system(size: 15, weight: selected ? .medium : .regular))
                    .foregroundStyle(WTheme.ink)
                    .lineLimit(1)
                Spacer(minLength: 8)
                if let count, count > 0 {
                    Text(WFormat.compact(count))
                        .font(WFont.meta(11.5, weight: .medium))
                        .monospacedDigit()
                        .foregroundStyle(selected ? WTheme.ink : WTheme.inkTertiary)
                        .contentTransition(.numericText())
                }
            }
            .padding(.horizontal, 14)
            .frame(height: 42)
            .background {
                if selected {
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .fill(WTheme.fill)
                        .matchedGeometryEffect(id: matchedID, in: namespace)
                }
            }
            .contentShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
        .buttonStyle(TapScaleStyle())
        .opacity(revealed ? 1 : 0)
        .offset(x: revealed ? 0 : -18)
        // Cap the stagger so long row lists don't cascade for seconds.
        .animation(
            .spring(response: 0.42, dampingFraction: 0.82)
                .delay(revealed ? 0.03 + min(Double(index), 14) * 0.024 : 0),
            value: revealed
        )
    }
}

/// End-of-list marker: the exact total for the current scope/search.
struct CRMEndMarker: View {
    let count: Int
    let singular: String
    let plural: String

    init(_ count: Int, _ singular: String, plural: String? = nil) {
        self.count = count
        self.singular = singular
        self.plural = plural ?? singular + "s"
    }

    var body: some View {
        HStack {
            Spacer()
            Text("\(count) \(count == 1 ? singular : plural)")
                .font(WFont.meta(10.5, weight: .medium))
                .monospacedDigit()
                .foregroundStyle(WTheme.inkTertiary)
            Spacer()
        }
        .padding(.vertical, 14)
        .listRowSeparator(.hidden)
        .listRowBackground(Color.clear)
    }
}

// MARK: - Chips and accents

/// Hairline chip keyed by a server hex color (stages, task types): the color
/// rides a leading dot, the label stays ink.
struct CRMColorChip: View {
    let text: String
    var hex: String?

    var body: some View {
        let color = CRMHex.color(hex) ?? WTheme.paused
        HStack(spacing: 5) {
            Circle()
                .fill(color)
                .frame(width: 6, height: 6)
            Text(text)
                .font(.system(size: 11.5, weight: .medium))
                .foregroundStyle(WTheme.inkSecondary)
                .lineLimit(1)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 3.5)
        .background(WTheme.raised, in: Capsule())
        .overlay(Capsule().strokeBorder(WTheme.hairlineStrong, lineWidth: 1))
    }
}

// MARK: - Field chrome

extension View {
    /// The search/input field surface: subtle band, hairline edge, 22pt radius.
    func crmFieldChrome(radius: CGFloat = 22) -> some View {
        background(WTheme.surface, in: RoundedRectangle(cornerRadius: radius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: radius, style: .continuous)
                    .strokeBorder(WTheme.hairlineStrong, lineWidth: 1)
            )
    }
}
