import SwiftUI

/// Navigation drawer for the analytics browser, mirroring the mailboxes /
/// contacts / campaigns sidebars: an ink header on the canvas wash with live
/// totals as hairline chips, then the analytics sections as rows. The
/// selected row's fill slides between rows and rows cascade in on open.
struct AnalyticsSidebar: View {
    let store: AnalyticsStore
    let selection: AnalyticsScope
    let topInset: CGFloat
    let revealed: Bool
    let onSelect: (AnalyticsScope) -> Void

    @Namespace private var activeNS

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            hero
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    sectionLabel("Sections")
                    scopeRows
                }
                .padding(.horizontal, 12)
                .padding(.top, 8)
                .padding(.bottom, 40)
            }
        }
        .background(WTheme.canvas)
    }

    // MARK: Hero

    private var hero: some View {
        DrawerWorkspaceHeader(topInset: topInset)
    }

    private func sectionLabel(_ text: String) -> some View {
        EyebrowLabel(text)
            .padding(.horizontal, 14)
            .padding(.top, 14)
            .padding(.bottom, 6)
    }

    // MARK: Rows

    @ViewBuilder
    private var scopeRows: some View {
        ForEach(Array(AnalyticsScope.allCases.enumerated()), id: \.element) { index, scope in
            row(index: index, scope: scope)
        }
    }

    private func row(index: Int, scope: AnalyticsScope) -> some View {
        let selected = selection == scope
        // Only the accounts row carries a count; rose when any account has issues.
        let count = scope == .accounts ? store.accounts.count : 0
        let countColor: Color? = scope == .accounts && store.issueCount > 0 ? Tone.rose.color : nil
        return Button {
            onSelect(scope)
        } label: {
            HStack(spacing: 12) {
                Image(systemName: scope.icon)
                    .font(.system(size: 14, weight: .regular))
                    .foregroundStyle(selected ? WTheme.ink : WTheme.inkTertiary)
                    .frame(width: 22)
                Text(scope.title)
                    .font(.system(size: 15, weight: selected ? .medium : .regular))
                    .foregroundStyle(selected ? WTheme.ink : WTheme.inkSecondary)
                    .lineLimit(1)
                Spacer(minLength: 8)
                if count > 0 {
                    Text(WFormat.compact(count))
                        .font(WFont.meta(12, weight: .medium))
                        .monospacedDigit()
                        .foregroundStyle(countColor ?? (selected ? WTheme.ink : WTheme.inkTertiary))
                        .contentTransition(.numericText())
                }
            }
            .padding(.horizontal, 14)
            .frame(height: 42)
            .background {
                if selected {
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .fill(WTheme.fill)
                        .matchedGeometryEffect(id: "analyticsdrawer-active", in: activeNS)
                }
            }
            .contentShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
        .buttonStyle(TapScaleStyle())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(count > 0 ? "\(scope.title), \(count)" : scope.title)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .opacity(revealed ? 1 : 0)
        .offset(x: revealed ? 0 : -18)
        .animation(
            .spring(response: 0.42, dampingFraction: 0.82)
                .delay(revealed ? 0.03 + min(Double(index), 14) * 0.024 : 0),
            value: revealed
        )
    }
}
