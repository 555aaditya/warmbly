import SwiftUI

/// Navigation drawer for the contacts browser, mirroring the campaigns/unibox
/// sidebars: an ink wordmark header with hairline count chips, then browse
/// scopes and the member's categories as quiet rows on the canvas. Counts come from
/// the search `counts` block; categories from the session. The selected fill
/// slides between rows and rows cascade in when the drawer opens. Presented
/// full-bleed by `ContactsRootView`.
struct ContactsSidebar: View {
    let store: ContactsStore
    /// The member's contact categories (session), in position order.
    let categories: [ContactCategory]
    let selection: ContactBrowseScope
    let topInset: CGFloat
    let revealed: Bool
    let onSelect: (ContactBrowseScope) -> Void

    @Namespace private var activeNS

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            hero
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    scopeRows
                    if !categories.isEmpty {
                        sectionLabel("Categories")
                        categoryRows
                    }
                }
                .padding(.horizontal, 12)
                .padding(.top, 12)
                .padding(.bottom, 40)
            }
            .background(WTheme.canvas)
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
            .padding(.top, 18)
            .padding(.bottom, 6)
    }

    // MARK: Rows

    @ViewBuilder
    private var scopeRows: some View {
        row(index: 0, icon: "person.2", title: "All contacts", count: store.allCount, selected: selection == .all) {
            onSelect(.all)
        }
        row(
            index: 1,
            icon: "checkmark.seal",
            title: "Subscribed",
            count: store.subscribedCount,
            selected: selection == .subscribed,
            countColor: WTheme.positive
        ) {
            onSelect(.subscribed)
        }
        row(
            index: 2,
            icon: "bell.slash",
            title: "Unsubscribed",
            count: store.unsubscribedCount,
            selected: selection == .unsubscribed
        ) {
            onSelect(.unsubscribed)
        }
        row(index: 3, icon: "paperplane", title: "In a campaign", count: store.inCampaignCount, selected: selection == .inCampaign) {
            onSelect(.inCampaign)
        }
        row(index: 4, icon: "circle.dashed", title: "Not contacted", count: store.notContactedCount, selected: selection == .notContacted) {
            onSelect(.notContacted)
        }
    }

    @ViewBuilder
    private var categoryRows: some View {
        ForEach(Array(categories.enumerated()), id: \.element.id) { offset, category in
            let label = category.title ?? "Category"
            let scope = ContactBrowseScope.category(id: category.id, label: label)
            row(
                index: 5 + offset,
                dot: ContactColor.dot(category.color, seed: category.id),
                title: label,
                count: store.categoryCount(category.id),
                selected: selection == scope
            ) {
                onSelect(scope)
            }
        }
    }

    private func row(
        index: Int,
        icon: String? = nil,
        dot: Color? = nil,
        title: String,
        count: Int,
        selected: Bool,
        countColor: Color? = nil,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            HStack(spacing: 13) {
                Group {
                    if let dot {
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
                if count > 0 {
                    HStack(spacing: 5) {
                        if let countColor {
                            Circle()
                                .fill(countColor)
                                .frame(width: 5, height: 5)
                        }
                        Text(WFormat.compact(count))
                            .font(WFont.meta(11.5, weight: .medium))
                            .monospacedDigit()
                            .foregroundStyle(selected ? WTheme.ink : WTheme.inkTertiary)
                            .contentTransition(.numericText())
                    }
                }
            }
            .padding(.horizontal, 14)
            .frame(height: 42)
            .background {
                if selected {
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .fill(WTheme.fill)
                        .matchedGeometryEffect(id: "drawer-active", in: activeNS)
                }
            }
            .contentShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
        .buttonStyle(TapScaleStyle())
        .opacity(revealed ? 1 : 0)
        .offset(x: revealed ? 0 : -18)
        // Cap the stagger so a long category list doesn't cascade for seconds.
        .animation(
            .spring(response: 0.42, dampingFraction: 0.82)
                .delay(revealed ? 0.03 + min(Double(index), 14) * 0.022 : 0),
            value: revealed
        )
    }
}
