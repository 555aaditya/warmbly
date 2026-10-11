import SwiftUI

/// Navigation drawer for the campaign Leads browser, mirroring the contacts /
/// campaigns / unibox sidebars: an ink header with the campaign name and live
/// lead totals as hairline chips, then the status scopes as pill rows on the
/// canvas. Counts come from the search `lead_counts` block. The selected pill
/// slides between rows and rows cascade in when the drawer opens.
struct CampaignLeadsSidebar: View {
    let store: CampaignLeadsStore
    let campaignName: String
    let selection: CampaignLeadScope
    let topInset: CGFloat
    let revealed: Bool
    let onSelect: (CampaignLeadScope) -> Void

    @Namespace private var activeNS

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            hero
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    sectionLabel("Status")
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
        Text("Leads")
            .displayFont(28, weight: .semibold)
            .foregroundStyle(WTheme.ink)
            .padding(.horizontal, 26)
            .padding(.top, topInset + 14)
            .padding(.bottom, 6)
            .frame(maxWidth: .infinity, alignment: .leading)
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
        ForEach(Array(CampaignLeadScope.allCases.enumerated()), id: \.element) { index, scope in
            row(index: index, scope: scope)
        }
    }

    private func row(index: Int, scope: CampaignLeadScope) -> some View {
        let selected = selection == scope
        let count = store.scopeCount(scope)
        let live = scope == .processing && count > 0
        return Button {
            onSelect(scope)
        } label: {
            HStack(spacing: 13) {
                Group {
                    if live {
                        Circle()
                            .fill(scope.tone.color)
                            .frame(width: 8, height: 8)
                            .modifier(PingEffect(active: true, color: scope.tone.color))
                    } else {
                        Image(systemName: scope.icon)
                            .font(.system(size: 14, weight: .regular))
                            .foregroundStyle(selected ? WTheme.ink : WTheme.inkTertiary)
                    }
                }
                .frame(width: 24)
                Text(scope.title)
                    .font(.system(size: 14.5, weight: selected ? .medium : .regular))
                    .foregroundStyle(selected ? WTheme.ink : WTheme.inkSecondary)
                    .lineLimit(1)
                Spacer(minLength: 8)
                if count > 0 {
                    Text(WFormat.compact(count))
                        .font(WFont.meta(11.5, weight: .medium))
                        .foregroundStyle(selected ? WTheme.ink : WTheme.inkTertiary)
                        .contentTransition(.numericText())
                }
            }
            .padding(.horizontal, 16)
            .frame(height: 44)
            .background {
                if selected {
                    Capsule()
                        .fill(WTheme.fill)
                        .matchedGeometryEffect(id: "leaddrawer-active", in: activeNS)
                }
            }
            .contentShape(Capsule())
        }
        .buttonStyle(TapScaleStyle())
        .opacity(revealed ? 1 : 0)
        .offset(x: revealed ? 0 : -18)
        .animation(
            .spring(response: 0.42, dampingFraction: 0.82)
                .delay(revealed ? 0.03 + min(Double(index), 14) * 0.024 : 0),
            value: revealed
        )
    }
}
