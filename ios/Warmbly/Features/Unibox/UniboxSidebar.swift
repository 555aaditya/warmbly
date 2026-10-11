import SwiftUI

/// Navigation drawer for the unibox, the web's scope rail on a phone:
/// Favorites, Mail, Views, Mailboxes, Labels and Tags as sections that fold,
/// move and filter, with rows that can be starred, renamed, hidden and
/// reordered. Counts come from `GET /v1/unibox/overview`; the layout follows
/// the account through `UniboxRailStore`. Presented full-bleed by `UniboxRootView`.
struct UniboxSidebar: View {
    let store: UniboxStore
    let rail: UniboxRailStore
    /// All of the member's categories (session), merged with overview unread counts.
    let labels: [UniboxGroupCount]
    let selection: UniboxScope
    let topInset: CGFloat
    let revealed: Bool
    let canCompose: Bool
    let canMarkRead: Bool
    let onSelect: (UniboxScope) -> Void
    let onScheduled: () -> Void
    let onDrafts: () -> Void

    @Environment(AppEnvironment.self) private var env
    @Namespace private var activeNS
    /// Per-section filter for long lists (mailboxes, labels, tags).
    @State private var filters: [String: String] = [:]
    @State private var expanded: Set<String> = []
    /// The active row was picked from its home section, not from Favorites.
    @State private var homePick = false
    @State private var renameTarget: UniboxRailRow?
    @State private var renameText = ""
    @State private var editorSection: EditorTarget?
    @State private var starPulse = 0

    private struct EditorTarget: Identifiable {
        let section: String
        var id: String { section }
    }

    private static let collapseThreshold = 8
    private static let collapsedCount = 6

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            DrawerWorkspaceHeader(topInset: topInset)
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    if canCompose, store.composeDraftCount > 0 {
                        draftsRow
                    }
                    ForEach(presentSections, id: \.self) { section in
                        sectionView(section)
                            .transition(.opacity)
                    }
                    customizeButton
                }
                .padding(.horizontal, 12)
                .padding(.top, 4)
                .padding(.bottom, 40)
                .animation(.spring(response: 0.38, dampingFraction: 0.86), value: rail.layout)
                .animation(.spring(response: 0.38, dampingFraction: 0.86), value: rail.folded)
            }
            .scrollIndicators(.hidden)
            .scrollDismissesKeyboard(.immediately)
            .mask {
                // Rows dissolve under the header instead of being cut off.
                VStack(spacing: 0) {
                    LinearGradient(colors: [.clear, .black], startPoint: .top, endPoint: .bottom).frame(height: 12)
                    Rectangle()
                }
            }
        }
        .background(WTheme.canvas)
        .sensoryFeedback(.impact(weight: .light), trigger: starPulse)
        .alert("Rename favorite", isPresented: Binding(
            get: { renameTarget != nil },
            set: { if !$0 { renameTarget = nil } }
        )) {
            TextField(renameTarget?.label ?? "Name", text: $renameText)
            Button("Save") {
                if let row = renameTarget {
                    withAnimation(.snappy) { rail.rename(row.key, to: renameText, original: row.label) }
                }
                renameTarget = nil
            }
            Button("Cancel", role: .cancel) { renameTarget = nil }
        } message: {
            Text("Leave it empty to use \(renameTarget?.label ?? "its own name") again.")
        }
        .sheet(item: $editorSection) { target in
            UniboxRailEditor(
                rail: rail,
                rows: rowsByKey,
                presentSections: presentSections,
                focus: target.section
            )
        }
    }

    // MARK: Rows

    private var activeKey: String { selection.key }

    private func mailRow(_ key: String) -> UniboxRailRow? {
        let overview = store.overview
        switch key {
        case "all":
            return UniboxRailRow(key: key, label: "All mail", symbol: "square.stack", count: overview?.total ?? 0, scope: .all)
        case "unread":
            let unread = overview?.unread ?? 0
            return UniboxRailRow(key: key, label: "Unread", symbol: "envelope.badge", count: unread, accent: unread > 0, scope: .unread)
        case "awaiting":
            let value = overview?.awaitingReply ?? 0
            return UniboxRailRow(key: key, label: "Awaiting reply", symbol: "arrowshape.turn.up.left",
                                 count: value > 0 ? value : nil, accent: value > 0, scope: .awaiting)
        case "agent_drafts":
            let value = overview?.awaitingAgentDraft ?? 0
            return UniboxRailRow(key: key, label: "Agent drafts", symbol: "sparkles",
                                 count: value > 0 ? value : nil, accent: value > 0, scope: .agentDrafts)
        case "snoozed":
            let value = overview?.snoozed ?? 0
            return UniboxRailRow(key: key, label: "Snoozed", symbol: "moon.zzz", count: value > 0 ? value : nil, scope: .snoozed)
        case "scheduled":
            let value = overview?.scheduledPending ?? 0
            return UniboxRailRow(key: key, label: "Scheduled", symbol: "clock",
                                 count: value > 0 ? value : nil, accent: value > 0, scope: .scheduled)
        default:
            guard key.hasPrefix("folder:") else { return nil }
            let id = String(key.dropFirst("folder:".count))
            let counts = store.folderCount(id)
            let raw = id == "drafts" ? (counts?.total ?? 0) : (counts?.unread ?? 0)
            return UniboxRailRow(
                key: key,
                label: UniboxRail.folderTitle(id),
                symbol: UniboxRail.folderSymbol(id),
                count: raw > 0 ? raw : nil,
                accent: id != "drafts" && raw > 0,
                scope: .folder(id)
            )
        }
    }

    private func viewRow(_ key: String) -> UniboxRailRow? {
        guard let view = UniboxRail.views.first(where: { "view:\($0.id)" == key }) else { return nil }
        let unread = store.viewUnread(view)
        return UniboxRailRow(key: key, label: view.label, symbol: view.symbol, count: unread > 0 ? unread : nil,
                             accent: unread > 0, meaning: view.meaning, scope: .view(view.id))
    }

    private var mailboxRows: [UniboxRailRow] {
        store.mailboxes.map { mailbox in
            let named = mailbox.name?.isEmpty == false
            let label = named ? (mailbox.name ?? "") : (mailbox.email ?? "Mailbox")
            let unread = mailbox.unread ?? 0
            return UniboxRailRow(key: "mailbox:\(mailbox.id)", label: label, symbol: "envelope.open",
                                 count: unread > 0 ? unread : nil, accent: unread > 0,
                                 meaning: named ? mailbox.email : nil,
                                 scope: .mailbox(id: mailbox.id, label: label))
        }
    }

    private var labelRows: [UniboxRailRow] {
        labels.map { category in
            let label = category.title ?? "Label"
            let unread = category.unread ?? 0
            let count = unread > 0 ? unread : (category.total ?? 0)
            return UniboxRailRow(key: "category:\(category.id)", label: label,
                                 dot: Color(uniboxHex: category.color) ?? Color(hex: 0x94A3B8),
                                 count: count > 0 ? count : nil, accent: unread > 0,
                                 meaning: UniboxRail.labelMeanings[label.lowercased()],
                                 scope: .category(id: category.id, label: label, colorHex: category.color))
        }
    }

    private var tagRows: [UniboxRailRow] {
        store.tags.map { tag in
            let label = tag.title ?? "Tag"
            let unread = tag.unread ?? 0
            let count = unread > 0 ? unread : (tag.total ?? 0)
            return UniboxRailRow(key: "tag:\(tag.id)", label: label,
                                 dot: Color(uniboxHex: tag.color) ?? Color(hex: 0x94A3B8),
                                 count: count > 0 ? count : nil, accent: unread > 0,
                                 scope: .tag(id: tag.id, label: label))
        }
    }

    /// Every row a favorite can point at.
    private var rowsByKey: [String: UniboxRailRow] {
        var rows: [String: UniboxRailRow] = [:]
        for key in UniboxRail.mailKeys { if let row = mailRow(key) { rows[key] = row } }
        if store.overview != nil {
            for key in UniboxRail.viewKeys { if let row = viewRow(key) { rows[key] = row } }
        }
        for row in mailboxRows + labelRows + tagRows { rows[row.key] = row }
        return rows
    }

    private func allRows(_ section: String) -> [UniboxRailRow] {
        switch section {
        case "favorites":
            let lookup = rowsByKey
            return rail.layout.favorites.compactMap { favorite in
                guard let row = lookup[favorite.key] else { return nil }
                guard let name = favorite.name, !name.isEmpty else { return row }
                return UniboxRailRow(key: row.key, label: name, symbol: row.symbol, dot: row.dot, count: row.count,
                                     accent: row.accent, meaning: row.meaning.map { "\(row.label): \($0)" } ?? row.label,
                                     scope: row.scope)
            }
        case "mail": return rail.rowOrder("mail").compactMap(mailRow)
        case "views": return store.overview == nil ? [] : rail.rowOrder("views").compactMap(viewRow)
        case "mailboxes": return mailboxRows
        case "labels": return labelRows
        default: return tagRows
        }
    }

    /// Rows a section draws: hidden rows drop out unless active, the filter applies.
    private func visibleRows(_ section: String) -> [UniboxRailRow] {
        var rows = allRows(section)
        if section == "mail" || section == "views" {
            rows = rows.filter { !rail.isHidden($0.key) || $0.key == activeKey }
        }
        let needle = (filters[section] ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        if !needle.isEmpty {
            rows = rows.filter { $0.label.lowercased().contains(needle) || ($0.meaning ?? "").lowercased().contains(needle) }
        }
        return rows
    }

    // MARK: Sections

    private func isFiltering(_ section: String) -> Bool {
        !(filters[section] ?? "").trimmingCharacters(in: .whitespaces).isEmpty
    }

    private var presentSections: [String] {
        rail.sectionOrder().filter { section in
            switch section {
            case "favorites": !allRows(section).isEmpty
            case "views": store.categories.contains { UniboxRail.labelMeanings[($0.title ?? "").lowercased()] != nil }
            case "labels": !labels.isEmpty
            case "tags": !store.tags.isEmpty
            default: true
            }
        }
    }

    private func homeSection(_ key: String) -> String {
        if UniboxRail.mailKeys.contains(key) { return "mail" }
        if key.hasPrefix("view:") { return "views" }
        if key.hasPrefix("mailbox:") { return "mailboxes" }
        if key.hasPrefix("tag:") { return "tags" }
        return "labels"
    }

    /// Where the highlight sits: Favorites, unless the row was picked at home.
    private var highlightSection: String {
        if rail.isFavorite(activeKey), presentSections.contains("favorites"), !homePick { return "favorites" }
        return homeSection(activeKey)
    }

    @ViewBuilder
    private func sectionView(_ section: String) -> some View {
        let rows = visibleRows(section)
        let filtering = isFiltering(section)
        let folded = rail.isFolded(section) && !filtering
        let listSection = ["mailboxes", "labels", "tags"].contains(section)
        let searchable = listSection && (allRows(section).count > Self.collapseThreshold || filtering)
        let long = listSection && rows.count > Self.collapseThreshold && !expanded.contains(section) && !filtering
        let shown: [UniboxRailRow] = folded
            ? rows.filter { $0.key == activeKey && highlightSection == section }
            : (long ? Array(rows.prefix(Self.collapsedCount)) : rows)

        VStack(alignment: .leading, spacing: 2) {
            sectionHeader(section, rows: rows, folded: folded)
            if searchable, !folded {
                sectionFilter(section)
            }
            if filtering, rows.isEmpty {
                Text("No matches.")
                    .font(.system(size: 14.5))
                    .foregroundStyle(WTheme.inkTertiary)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 8)
            }
            ForEach(Array(shown.enumerated()), id: \.element.id) { offset, row in
                railRow(row, section: section, index: offset, siblings: rows.map(\.key))
                    .transition(.opacity.combined(with: .move(edge: .top)))
            }
            if listSection, !folded, !filtering, rows.count > Self.collapseThreshold {
                Button {
                    withAnimation(.spring(response: 0.38, dampingFraction: 0.86)) {
                        if expanded.contains(section) { expanded.remove(section) } else { expanded.insert(section) }
                    }
                } label: {
                    Text(expanded.contains(section) ? "Show less" : "Show all (\(rows.count - Self.collapsedCount) more)")
                        .font(.system(size: 14, weight: .medium))
                        .foregroundStyle(WTheme.accent)
                        .padding(.horizontal, 14)
                        .frame(height: 34)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
            if section == "mail", !folded { queueMeter }
        }
        .padding(.bottom, 6)
    }

    private func sectionHeader(_ section: String, rows: [UniboxRailRow], folded: Bool) -> some View {
        let hiddenAccent = folded && rows.contains { $0.accent && $0.key != activeKey }
        let hidden = (section == "mail" || section == "views") ? rail.hiddenCount(section) : 0
        return HStack(spacing: 6) {
            Button {
                withAnimation(.spring(response: 0.38, dampingFraction: 0.86)) {
                    rail.setFolded(section, !rail.isFolded(section))
                }
            } label: {
                HStack(spacing: 6) {
                    Text(UniboxRail.sectionTitle(section))
                        .font(.system(size: 14, weight: .semibold))
                        .foregroundStyle(WTheme.inkSecondary)
                    Image(systemName: "chevron.down")
                        .font(.system(size: 10, weight: .bold))
                        .foregroundStyle(WTheme.inkTertiary)
                        .rotationEffect(.degrees(folded ? -90 : 0))
                    if hiddenAccent {
                        Circle()
                            .fill(WTheme.accent)
                            .frame(width: 6, height: 6)
                            .transition(.scale.combined(with: .opacity))
                            .accessibilityLabel("Highlighted count folded away")
                    }
                    Spacer(minLength: 6)
                    if ["mailboxes", "labels", "tags"].contains(section) {
                        Text("\(rows.count)")
                            .font(.system(size: 13, weight: .medium))
                            .monospacedDigit()
                            .foregroundStyle(WTheme.inkTertiary)
                    } else if hidden > 0 {
                        Text("\(hidden) hidden")
                            .font(.system(size: 13))
                            .foregroundStyle(WTheme.inkTertiary)
                            .transition(.scale.combined(with: .opacity))
                    }
                }
                .frame(height: 36)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            Menu {
                sectionMenu(section)
            } label: {
                Image(systemName: "ellipsis")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(WTheme.inkTertiary)
                    .frame(width: 30, height: 30)
                    .contentShape(Rectangle())
            }
            .accessibilityLabel("\(UniboxRail.sectionTitle(section)) options")
        }
        .padding(.leading, 14)
        .padding(.trailing, 4)
        .padding(.top, 12)
    }

    @ViewBuilder
    private func sectionMenu(_ section: String) -> some View {
        let present = presentSections
        let folded = rail.isFolded(section)
        Button {
            withAnimation(.spring(response: 0.38, dampingFraction: 0.86)) { rail.setFolded(section, !folded) }
        } label: {
            Label(folded ? "Unfold section" : "Fold section", systemImage: folded ? "chevron.down" : "chevron.up")
        }
        Button {
            withAnimation(.spring(response: 0.38, dampingFraction: 0.86)) { rail.foldOthers(than: section, present: present) }
        } label: {
            Label("Fold other sections", systemImage: "rectangle.compress.vertical")
        }
        .disabled(!present.contains { $0 != section && !rail.isFolded($0) })
        Button {
            withAnimation(.spring(response: 0.38, dampingFraction: 0.86)) { rail.unfoldAll() }
        } label: {
            Label("Unfold all sections", systemImage: "rectangle.expand.vertical")
        }
        .disabled(!present.contains { rail.isFolded($0) })
        Divider()
        Button {
            rail.moveSection(section, by: -1, present: present)
        } label: {
            Label("Move section up", systemImage: "arrow.up")
        }
        .disabled(present.first == section)
        Button {
            rail.moveSection(section, by: 1, present: present)
        } label: {
            Label("Move section down", systemImage: "arrow.down")
        }
        .disabled(present.last == section)
        if section == "mail" || section == "views" {
            Divider()
            Button {
                editorSection = EditorTarget(section: section)
            } label: {
                Label("Edit rows…", systemImage: "slider.horizontal.3")
            }
            let hidden = rail.hiddenCount(section)
            if hidden > 0 {
                Button {
                    for key in UniboxRailStore.defaults(for: section) where rail.isHidden(key) {
                        rail.setHidden(key, false)
                    }
                } label: {
                    Label(hidden == 1 ? "Show 1 hidden row" : "Show \(hidden) hidden rows", systemImage: "eye")
                }
            }
            Button {
                rail.reset(section)
            } label: {
                Label("Reset to default", systemImage: "arrow.counterclockwise")
            }
            .disabled(!rail.isCustomized(section))
        }
        if section == "favorites" {
            Divider()
            Button {
                editorSection = EditorTarget(section: section)
            } label: {
                Label("Edit favorites…", systemImage: "slider.horizontal.3")
            }
            Button(role: .destructive) {
                rail.clearFavorites()
            } label: {
                Label("Remove all favorites", systemImage: "star.slash")
            }
        }
    }

    private func sectionFilter(_ section: String) -> some View {
        let binding = Binding(get: { filters[section] ?? "" }, set: { filters[section] = $0 })
        return HStack(spacing: 7) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(WTheme.inkTertiary)
            TextField(text: binding) {
                Text("Filter \(UniboxRail.sectionTitle(section).lowercased())").foregroundStyle(WTheme.inkTertiary)
            }
            .font(.system(size: 15))
            .foregroundStyle(WTheme.ink)
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            if !binding.wrappedValue.isEmpty {
                Button {
                    filters[section] = ""
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 14))
                        .foregroundStyle(WTheme.inkTertiary)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Clear filter")
            }
        }
        .padding(.horizontal, 11)
        .frame(height: 34)
        .background(WTheme.fill, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        .padding(.horizontal, 6)
        .padding(.bottom, 4)
    }

    // MARK: Row

    private func railRow(_ row: UniboxRailRow, section: String, index: Int, siblings: [String]) -> some View {
        let selected = row.key == activeKey && highlightSection == section
        return Button {
            if row.key == "scheduled" {
                onScheduled()
            } else {
                pick(row.scope, from: section)
            }
        } label: {
            HStack(spacing: 13) {
                Group {
                    if let dot = row.dot {
                        Circle()
                            .fill(dot)
                            .frame(width: 9, height: 9)
                    } else if let symbol = row.symbol {
                        Image(systemName: symbol)
                            .font(.system(size: 16, weight: .regular))
                            .symbolVariant(selected ? .fill : .none)
                            .foregroundStyle(selected ? WTheme.ink : WTheme.inkSecondary)
                    }
                }
                .frame(width: 24)
                Text(row.label)
                    .font(.system(size: 16, weight: selected ? .medium : .regular))
                    .foregroundStyle(WTheme.ink)
                    .lineLimit(1)
                if section != "favorites", rail.isFavorite(row.key) {
                    Image(systemName: "star.fill")
                        .font(.system(size: 9, weight: .semibold))
                        .foregroundStyle(Tone.amber.color)
                        .transition(.scale(scale: 0.4).combined(with: .opacity))
                }
                Spacer(minLength: 8)
                if let count = row.count {
                    Text(WFormat.compact(count))
                        .font(.system(size: 14, weight: row.accent ? .semibold : .medium))
                        .monospacedDigit()
                        .foregroundStyle(row.accent ? WTheme.accent : WTheme.inkTertiary)
                        .contentTransition(.numericText(value: Double(count)))
                }
                if row.key == "scheduled" {
                    Image(systemName: "chevron.right")
                        .font(.system(size: 11, weight: .medium))
                        .foregroundStyle(WTheme.inkTertiary)
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
            .contentShape(.contextMenuPreview, RoundedRectangle(cornerRadius: 12, style: .continuous))
            .contentShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
        .buttonStyle(TapScaleStyle())
        .contextMenu { rowMenu(row, section: section, siblings: siblings) }
        .opacity(revealed ? 1 : 0)
        .offset(x: revealed ? 0 : -18)
        // Cap the stagger so long lists don't cascade for seconds.
        .animation(
            .spring(response: 0.42, dampingFraction: 0.82)
                .delay(revealed ? 0.03 + min(Double(index), 14) * 0.022 : 0),
            value: revealed
        )
    }

    @ViewBuilder
    private func rowMenu(_ row: UniboxRailRow, section: String, siblings: [String]) -> some View {
        let orderable = section == "mail" || section == "views" || section == "favorites"
        if let meaning = row.meaning {
            Section(meaning) {}
        }
        if let folder = row.folder, canMarkRead {
            Button {
                Task { await store.markFolderRead(env.api, folder: folder) }
            } label: {
                Label("Mark all as read", systemImage: "envelope.open")
            }
        }
        if orderable {
            Section {
                Button {
                    move(row.key, in: section, by: -1, siblings: siblings)
                } label: {
                    Label("Move up", systemImage: "arrow.up")
                }
                .disabled(siblings.first == row.key)
                Button {
                    move(row.key, in: section, by: 1, siblings: siblings)
                } label: {
                    Label("Move down", systemImage: "arrow.down")
                }
                .disabled(siblings.last == row.key)
            }
        }
        if section == "favorites" {
            Button {
                renameText = rail.layout.favorites.first { $0.key == row.key }?.name ?? ""
                renameTarget = rowsByKey[row.key] ?? row
            } label: {
                Label("Rename…", systemImage: "pencil")
            }
        }
        if section == "mail" || section == "views" {
            Button {
                rail.setHidden(row.key, true)
            } label: {
                Label("Hide from sidebar", systemImage: "eye.slash")
            }
        }
        Button {
            starPulse += 1
            withAnimation(.spring(response: 0.35, dampingFraction: 0.6)) { rail.toggleFavorite(row.key) }
        } label: {
            if rail.isFavorite(row.key) {
                Label("Remove from Favorites", systemImage: "star.slash")
            } else {
                Label("Add to Favorites", systemImage: "star")
            }
        }
        if orderable {
            Divider()
            Button {
                editorSection = EditorTarget(section: section)
            } label: {
                Label(section == "favorites" ? "Edit favorites…" : "Edit \(UniboxRail.sectionTitle(section)) rows…",
                      systemImage: "slider.horizontal.3")
            }
        }
    }

    private func move(_ key: String, in section: String, by offset: Int, siblings: [String]) {
        if section == "favorites" {
            rail.moveFavorite(key, by: offset, visible: siblings)
        } else {
            rail.moveRow(section, key: key, by: offset, visible: siblings)
        }
    }

    private func pick(_ scope: UniboxScope, from section: String) {
        homePick = section != "favorites"
        onSelect(scope)
    }

    // MARK: Extras

    private var draftsRow: some View {
        Button(action: onDrafts) {
            HStack(spacing: 13) {
                Image(systemName: "square.and.pencil")
                    .font(.system(size: 16))
                    .foregroundStyle(WTheme.inkSecondary)
                    .frame(width: 24)
                Text("Unsent drafts")
                    .font(.system(size: 16))
                    .foregroundStyle(WTheme.ink)
                Spacer(minLength: 8)
                Text(WFormat.compact(store.composeDraftCount))
                    .font(.system(size: 14, weight: .medium))
                    .monospacedDigit()
                    .foregroundStyle(WTheme.inkTertiary)
                    .contentTransition(.numericText())
                Image(systemName: "chevron.right")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(WTheme.inkTertiary)
            }
            .padding(.horizontal, 14)
            .frame(height: 42)
            .contentShape(Rectangle())
        }
        .buttonStyle(TapScaleStyle())
        .padding(.top, 4)
    }

    /// The scheduled-send queue, once it is at least 70% of the workspace cap.
    @ViewBuilder
    private var queueMeter: some View {
        let used = store.overview?.scheduledPending ?? 0
        let cap = store.overview?.scheduledPendingMax ?? 0
        if cap > 0, Double(used) / Double(cap) >= 0.7 {
            let ratio = min(1, Double(used) / Double(cap))
            let tint = ratio >= 0.95 ? Tone.rose.color : (ratio >= 0.85 ? Tone.amber.color : WTheme.accent)
            VStack(alignment: .leading, spacing: 7) {
                HStack {
                    Text("Scheduled queue")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundStyle(WTheme.inkSecondary)
                    Spacer()
                    Text("\(used)/\(cap)")
                        .font(.system(size: 13, weight: .semibold))
                        .monospacedDigit()
                        .foregroundStyle(tint)
                }
                GeometryReader { proxy in
                    ZStack(alignment: .leading) {
                        Capsule().fill(WTheme.fill)
                        Capsule().fill(tint).frame(width: proxy.size.width * ratio)
                    }
                }
                .frame(height: 5)
                if ratio >= 0.95 {
                    Text("Near the limit. Cancel a few sends to free up space.")
                        .font(.system(size: 12.5))
                        .foregroundStyle(WTheme.inkSecondary)
                }
            }
            .padding(.horizontal, 14)
            .padding(.top, 8)
            .padding(.bottom, 4)
        }
    }

    private var customizeButton: some View {
        Button {
            editorSection = EditorTarget(section: "sections")
        } label: {
            HStack(spacing: 8) {
                Image(systemName: "slider.horizontal.3")
                    .font(.system(size: 13.5, weight: .medium))
                Text("Customize sidebar")
                    .font(.system(size: 14.5, weight: .medium))
            }
            .foregroundStyle(WTheme.inkSecondary)
            .padding(.horizontal, 14)
            .frame(height: 40)
            .contentShape(Rectangle())
        }
        .buttonStyle(TapScaleStyle())
        .padding(.top, 14)
    }
}
