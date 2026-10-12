import SwiftUI

/// Navigation drawer for the campaigns browser, in the inbox drawer's shape:
/// the workspace header, then Status, Folders and Sort as sections that fold.
/// Folders are created, renamed, recolored and deleted here (`/v1/folders`,
/// like the web's Manage folders). Counts come from `GET /campaigns-overview`.
/// Presented full-bleed by `CampaignsRootView`.
struct CampaignsSidebar: View {
    let store: CampaignsStore
    /// The member's campaign folders (session), in position order.
    let folders: [UserGroup]
    let selection: CampaignListScope
    let topInset: CGFloat
    let revealed: Bool
    let onSelect: (CampaignListScope) -> Void

    @Environment(AppEnvironment.self) private var env
    @Namespace private var activeNS
    @AppStorage("campaigns.rail.folded") private var foldedRaw = ""
    @State private var naming: FolderNaming?
    @State private var nameText = ""
    @State private var deleting: UserGroup?
    @State private var folderError: String?

    private struct FolderNaming: Identifiable {
        let folder: UserGroup?
        var id: String { folder?.id ?? "new" }
    }

    static let palette: [(name: String, hex: String)] = [
        ("Sky", "#0EA5E9"), ("Emerald", "#10B981"), ("Amber", "#F59E0B"), ("Rose", "#F43F5E"),
        ("Violet", "#8B5CF6"), ("Orange", "#F97316"), ("Pink", "#EC4899"), ("Slate", "#64748B"),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            DrawerWorkspaceHeader(topInset: topInset)
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    section("status", title: "Status") { statusRows }
                    section("folders", title: "Folders", count: folders.count, menu: AnyView(folderSectionMenu)) { folderRows }
                    section("sort", title: "Sort", trailing: store.sort.title) { sortRows }
                }
                .padding(.horizontal, 12)
                .padding(.top, 4)
                .padding(.bottom, 40)
                .animation(.spring(response: 0.38, dampingFraction: 0.86), value: foldedRaw)
            }
            .scrollIndicators(.hidden)
        }
        .background(WTheme.canvas)
        .alert(naming?.folder == nil ? "New folder" : "Rename folder", isPresented: Binding(
            get: { naming != nil },
            set: { if !$0 { naming = nil } }
        )) {
            TextField("Folder name", text: $nameText)
            Button(naming?.folder == nil ? "Create" : "Save") {
                let target = naming
                naming = nil
                Task { await saveName(target?.folder) }
            }
            Button("Cancel", role: .cancel) { naming = nil }
        }
        .alert("Delete \(deleting?.name ?? "folder")?", isPresented: Binding(
            get: { deleting != nil },
            set: { if !$0 { deleting = nil } }
        )) {
            Button("Delete", role: .destructive) {
                let target = deleting
                deleting = nil
                if let target { Task { await delete(target) } }
            }
            Button("Cancel", role: .cancel) { deleting = nil }
        } message: {
            Text("Campaigns in it stay; they just leave the folder.")
        }
        .alert("Couldn't update the folder", isPresented: Binding(
            get: { folderError != nil },
            set: { if !$0 { folderError = nil } }
        )) {
            Button("OK", role: .cancel) { folderError = nil }
        } message: {
            Text(folderError ?? "")
        }
    }

    // MARK: Sections

    private var folded: Set<String> { Set(foldedRaw.split(separator: ",").map(String.init)) }

    private func toggleFold(_ id: String) {
        var set = folded
        if set.contains(id) { set.remove(id) } else { set.insert(id) }
        foldedRaw = set.sorted().joined(separator: ",")
    }

    private func section<Rows: View>(
        _ id: String,
        title: String,
        count: Int? = nil,
        trailing: String? = nil,
        menu: AnyView? = nil,
        @ViewBuilder rows: () -> Rows
    ) -> some View {
        let isFolded = folded.contains(id)
        return VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                Button {
                    withAnimation(.spring(response: 0.38, dampingFraction: 0.86)) { toggleFold(id) }
                } label: {
                    HStack(spacing: 6) {
                        Text(title)
                            .font(.system(size: 14, weight: .semibold))
                            .foregroundStyle(WTheme.inkSecondary)
                        Image(systemName: "chevron.down")
                            .font(.system(size: 10, weight: .bold))
                            .foregroundStyle(WTheme.inkTertiary)
                            .rotationEffect(.degrees(isFolded ? -90 : 0))
                        Spacer(minLength: 6)
                        if let count, count > 0 {
                            Text("\(count)")
                                .font(.system(size: 13, weight: .medium))
                                .monospacedDigit()
                                .foregroundStyle(WTheme.inkTertiary)
                        } else if let trailing, isFolded {
                            Text(trailing)
                                .font(.system(size: 13))
                                .foregroundStyle(WTheme.inkTertiary)
                        }
                    }
                    .frame(height: 36)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                if let menu {
                    Menu { menu } label: {
                        Image(systemName: "ellipsis")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(WTheme.inkTertiary)
                            .frame(width: 30, height: 30)
                            .contentShape(Rectangle())
                    }
                    .accessibilityLabel("\(title) options")
                }
            }
            .padding(.leading, 14)
            .padding(.trailing, menu == nil ? 14 : 4)
            .padding(.top, 12)

            if !isFolded {
                rows()
                    .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
        .padding(.bottom, 6)
    }

    // MARK: Status

    @ViewBuilder
    private var statusRows: some View {
        row(index: 0, symbol: "rectangle.stack", title: "All campaigns", count: store.allCount, selected: selection == .all) {
            onSelect(.all)
        }
        row(index: 1, symbol: "paperplane", title: "Sending", detail: "Sending now", count: store.runningCount,
            accent: WTheme.positive, selected: selection == .running) {
            onSelect(.running)
        }
        row(index: 2, symbol: "pause.circle", title: "Paused", detail: "Can resume", count: store.pausedCount,
            accent: Tone.amber.color, selected: selection == .paused) {
            onSelect(.paused)
        }
        row(index: 3, symbol: "doc", title: "Drafts", detail: "Not started", count: store.draftCount, selected: selection == .draft) {
            onSelect(.draft)
        }
        row(index: 4, symbol: "checkmark.circle", title: "Finished", count: store.finishedCount, selected: selection == .finished) {
            onSelect(.finished)
        }
    }

    // MARK: Folders

    @ViewBuilder
    private var folderRows: some View {
        ForEach(Array(folders.enumerated()), id: \.element.id) { offset, folder in
            let label = folder.name ?? "Folder"
            let scope = CampaignListScope.folder(id: folder.id, label: label)
            row(
                index: 5 + offset,
                dot: Color(uniboxHex: folder.color) ?? Color(hex: 0x94A3B8),
                title: label,
                count: store.folderCount(folder.id),
                selected: selection == scope
            ) {
                onSelect(scope)
            }
            .contextMenu { folderMenu(folder) }
        }
        Button {
            nameText = ""
            naming = FolderNaming(folder: nil)
        } label: {
            HStack(spacing: 13) {
                Image(systemName: "plus")
                    .font(.system(size: 15, weight: .medium))
                    .frame(width: 24)
                Text("New folder")
                    .font(.system(size: 16))
                Spacer()
            }
            .foregroundStyle(WTheme.accent)
            .padding(.horizontal, 14)
            .frame(height: 42)
            .contentShape(Rectangle())
        }
        .buttonStyle(TapScaleStyle())
    }

    @ViewBuilder
    private var folderSectionMenu: some View {
        Button {
            nameText = ""
            naming = FolderNaming(folder: nil)
        } label: {
            Label("New folder", systemImage: "folder.badge.plus")
        }
        Button {
            withAnimation(.spring(response: 0.38, dampingFraction: 0.86)) { toggleFold("folders") }
        } label: {
            Label(folded.contains("folders") ? "Unfold section" : "Fold section", systemImage: "chevron.up.chevron.down")
        }
    }

    @ViewBuilder
    private func folderMenu(_ folder: UserGroup) -> some View {
        Button {
            nameText = folder.name ?? ""
            naming = FolderNaming(folder: folder)
        } label: {
            Label("Rename…", systemImage: "pencil")
        }
        Menu {
            ForEach(Self.palette, id: \.hex) { swatch in
                Button {
                    Task { await recolor(folder, swatch.hex) }
                } label: {
                    if folder.color?.uppercased() == swatch.hex {
                        Label(swatch.name, systemImage: "checkmark")
                    } else {
                        Text(swatch.name)
                    }
                }
            }
        } label: {
            Label("Color", systemImage: "paintpalette")
        }
        Divider()
        Button(role: .destructive) {
            deleting = folder
        } label: {
            Label("Delete folder", systemImage: "trash")
        }
    }

    // MARK: Sort

    @ViewBuilder
    private var sortRows: some View {
        ForEach(Array(CampaignSort.allCases.enumerated()), id: \.element.id) { offset, sort in
            Button {
                withAnimation(.snappy) { store.sort = sort }
            } label: {
                HStack(spacing: 13) {
                    Image(systemName: sort == .name ? "textformat" : (sort == .newest ? "arrow.down" : "arrow.up"))
                        .font(.system(size: 15))
                        .foregroundStyle(WTheme.inkSecondary)
                        .frame(width: 24)
                    Text(sort.title)
                        .font(.system(size: 16))
                        .foregroundStyle(WTheme.ink)
                    Spacer()
                    if store.sort == sort {
                        Image(systemName: "checkmark")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(WTheme.accent)
                            .transition(.scale.combined(with: .opacity))
                    }
                }
                .padding(.horizontal, 14)
                .frame(height: 42)
                .contentShape(Rectangle())
            }
            .buttonStyle(TapScaleStyle())
            .opacity(revealed ? 1 : 0)
            .offset(x: revealed ? 0 : -18)
            .animation(.spring(response: 0.42, dampingFraction: 0.82).delay(revealed ? 0.2 + Double(offset) * 0.02 : 0), value: revealed)
        }
    }

    // MARK: Row

    private func row(
        index: Int,
        symbol: String? = nil,
        dot: Color? = nil,
        title: String,
        detail: String? = nil,
        count: Int,
        accent: Color? = nil,
        selected: Bool,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            HStack(spacing: 13) {
                Group {
                    if let dot {
                        Circle().fill(dot).frame(width: 9, height: 9)
                    } else if let symbol {
                        Image(systemName: symbol)
                            .font(.system(size: 16))
                            .symbolVariant(selected ? .fill : .none)
                            .foregroundStyle(selected ? WTheme.ink : WTheme.inkSecondary)
                    }
                }
                .frame(width: 24)
                Text(title)
                    .font(.system(size: 16, weight: selected ? .medium : .regular))
                    .foregroundStyle(WTheme.ink)
                    .lineLimit(1)
                Spacer(minLength: 8)
                if count > 0 {
                    Text(WFormat.compact(count))
                        .font(.system(size: 14, weight: accent == nil ? .medium : .semibold))
                        .monospacedDigit()
                        .foregroundStyle(accent ?? WTheme.inkTertiary)
                        .contentTransition(.numericText(value: Double(count)))
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
        .accessibilityHint(detail ?? "")
        .opacity(revealed ? 1 : 0)
        .offset(x: revealed ? 0 : -18)
        // Cap the stagger so a long folder list doesn't cascade for seconds.
        .animation(
            .spring(response: 0.42, dampingFraction: 0.82)
                .delay(revealed ? 0.03 + min(Double(index), 14) * 0.022 : 0),
            value: revealed
        )
    }

    // MARK: Folder writes

    private func saveName(_ folder: UserGroup?) async {
        let name = nameText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { return }
        do {
            if let folder {
                let _: UserGroup = try await env.api.patch("folders/\(folder.id)", body: FolderWrite(title: name))
            } else {
                let color = Self.palette[folders.count % Self.palette.count].hex
                let _: UserGroup = try await env.api.post("folders", body: FolderWrite(title: name, color: color))
            }
            await env.session.refreshUser()
        } catch {
            folderError = error.localizedDescription
        }
    }

    private func recolor(_ folder: UserGroup, _ hex: String) async {
        do {
            let _: UserGroup = try await env.api.patch("folders/\(folder.id)", body: FolderWrite(color: hex))
            await env.session.refreshUser()
        } catch {
            folderError = error.localizedDescription
        }
    }

    private func delete(_ folder: UserGroup) async {
        do {
            let _: EmptyBody = try await env.api.delete("folders/\(folder.id)")
            if case let .folder(id, _) = selection, id == folder.id { onSelect(.all) }
            await env.session.refreshUser()
            await store.loadOverview(env.api)
        } catch {
            folderError = error.localizedDescription
        }
    }
}

/// `POST /v1/folders` and `PATCH /v1/folders/:id`; absent fields stay as they are.
private struct FolderWrite: Encodable {
    var title: String?
    var color: String?
}
