import SwiftUI

/// "Customize sidebar": section order, favorites (rename, reorder, remove)
/// and the Mail and Views rows (reorder, hide, star). Every change lands on
/// the account layout as it is made, so there is nothing to save.
struct UniboxRailEditor: View {
    let rail: UniboxRailStore
    let rows: [String: UniboxRailRow]
    let presentSections: [String]
    let focus: String

    @Environment(\.dismiss) private var dismiss
    @State private var renameKey: String?
    @State private var renameText = ""

    var body: some View {
        NavigationStack {
            ScrollViewReader { proxy in
                List {
                    sectionsSection
                    favoritesSection
                    rowsSection("mail")
                    if presentSections.contains("views") {
                        rowsSection("views")
                    }
                }
                .environment(\.editMode, .constant(.active))
                .scrollContentBackground(.hidden)
                .background(WTheme.canvas)
                .onAppear {
                    guard focus != "sections" else { return }
                    proxy.scrollTo(focus, anchor: .top)
                }
            }
            .navigationTitle("Customize sidebar")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                        .fontWeight(.semibold)
                }
            }
            .alert("Rename favorite", isPresented: Binding(
                get: { renameKey != nil },
                set: { if !$0 { renameKey = nil } }
            )) {
                TextField(renameKey.flatMap { rows[$0]?.label } ?? "Name", text: $renameText)
                Button("Save") {
                    if let key = renameKey, let row = rows[key] {
                        rail.rename(key, to: renameText, original: row.label)
                    }
                    renameKey = nil
                }
                Button("Cancel", role: .cancel) { renameKey = nil }
            } message: {
                Text("Up to \(UniboxRail.favoriteNameMax) characters. Leave it empty to use the original name.")
            }
        }
        .presentationDragIndicator(.visible)
    }

    // MARK: Sections

    private var sectionsSection: some View {
        Section {
            ForEach(rail.sectionOrder(), id: \.self) { section in
                HStack {
                    Text(UniboxRail.sectionTitle(section))
                        .font(.system(size: 16))
                        .foregroundStyle(WTheme.ink)
                    Spacer()
                    if !presentSections.contains(section) {
                        Text(section == "favorites" ? "Empty" : "Nothing here yet")
                            .font(.system(size: 13.5))
                            .foregroundStyle(WTheme.inkTertiary)
                    }
                }
                .listRowBackground(WTheme.raised)
            }
            .onMove { from, to in
                var order = rail.sectionOrder()
                order.move(fromOffsets: from, toOffset: to)
                rail.setSectionOrder(order)
            }
        } header: {
            header("Sections")
        } footer: {
            Text("Drag to change the order sections appear in.")
        }
        .id("sections")
    }

    // MARK: Favorites

    private var favoritesSection: some View {
        Section {
            if rail.layout.favorites.isEmpty {
                Text("Star any row below, or long-press one in the sidebar, to pin it here.")
                    .font(.system(size: 14.5))
                    .foregroundStyle(WTheme.inkSecondary)
                    .listRowBackground(WTheme.raised)
            }
            ForEach(rail.layout.favorites) { favorite in
                let row = rows[favorite.key]
                HStack(spacing: 12) {
                    rowIcon(row)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(favorite.name ?? row?.label ?? "Unavailable")
                            .font(.system(size: 16))
                            .foregroundStyle(row == nil ? WTheme.inkTertiary : WTheme.ink)
                            .lineLimit(1)
                        if favorite.name != nil, let original = row?.label {
                            Text(original)
                                .font(.system(size: 12.5))
                                .foregroundStyle(WTheme.inkTertiary)
                                .lineLimit(1)
                        }
                    }
                    Spacer(minLength: 8)
                    Button {
                        renameText = favorite.name ?? ""
                        renameKey = favorite.key
                    } label: {
                        Image(systemName: "pencil")
                            .font(.system(size: 14, weight: .medium))
                            .foregroundStyle(WTheme.inkSecondary)
                            .frame(width: 32, height: 32)
                    }
                    .buttonStyle(.borderless)
                    .accessibilityLabel("Rename")
                    .disabled(row == nil)
                }
                .listRowBackground(WTheme.raised)
            }
            .onMove { from, to in
                var favorites = rail.layout.favorites
                favorites.move(fromOffsets: from, toOffset: to)
                rail.setFavorites(favorites)
            }
            .onDelete { offsets in
                var favorites = rail.layout.favorites
                favorites.remove(atOffsets: offsets)
                rail.setFavorites(favorites)
            }
        } header: {
            header("Favorites")
        } footer: {
            if !rail.layout.favorites.isEmpty {
                Text("Drag to reorder, swipe or tap the minus to remove.")
            }
        }
        .id("favorites")
    }

    // MARK: Mail and Views

    private func rowsSection(_ section: String) -> some View {
        Section {
            ForEach(rail.rowOrder(section), id: \.self) { key in
                let row = rows[key]
                let hidden = rail.isHidden(key)
                HStack(spacing: 12) {
                    Button {
                        withAnimation(.snappy) { rail.setHidden(key, !hidden) }
                    } label: {
                        Image(systemName: hidden ? "circle" : "checkmark.circle.fill")
                            .font(.system(size: 20))
                            .foregroundStyle(hidden ? WTheme.inkTertiary : WTheme.accent)
                            .contentTransition(.symbolEffect(.replace))
                    }
                    .buttonStyle(.borderless)
                    .accessibilityLabel(hidden ? "Show \(row?.label ?? key)" : "Hide \(row?.label ?? key)")
                    rowIcon(row)
                    Text(row?.label ?? key)
                        .font(.system(size: 16))
                        .foregroundStyle(hidden ? WTheme.inkTertiary : WTheme.ink)
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    Button {
                        withAnimation(.spring(response: 0.35, dampingFraction: 0.6)) { rail.toggleFavorite(key) }
                    } label: {
                        Image(systemName: rail.isFavorite(key) ? "star.fill" : "star")
                            .font(.system(size: 15, weight: .medium))
                            .foregroundStyle(rail.isFavorite(key) ? Tone.amber.color : WTheme.inkTertiary)
                            .frame(width: 32, height: 32)
                            .contentTransition(.symbolEffect(.replace))
                    }
                    .buttonStyle(.borderless)
                    .accessibilityLabel(rail.isFavorite(key) ? "Remove from Favorites" : "Add to Favorites")
                }
                .listRowBackground(WTheme.raised)
            }
            .onMove { from, to in
                var order = rail.rowOrder(section)
                order.move(fromOffsets: from, toOffset: to)
                rail.setRowOrder(section, order)
            }
        } header: {
            HStack {
                header(UniboxRail.sectionTitle(section))
                Spacer()
                if rail.isCustomized(section) {
                    Button("Reset") {
                        withAnimation(.snappy) { rail.reset(section) }
                    }
                    .font(.system(size: 13.5, weight: .medium))
                    .foregroundStyle(WTheme.accent)
                    .textCase(nil)
                }
            }
        } footer: {
            Text("Drag to reorder, untick to hide. Hidden rows still open from search and links.")
        }
        .id(section)
    }

    // MARK: Pieces

    private func header(_ title: String) -> some View {
        Text(title)
            .font(.system(size: 13.5, weight: .semibold))
            .foregroundStyle(WTheme.inkSecondary)
            .textCase(nil)
    }

    @ViewBuilder
    private func rowIcon(_ row: UniboxRailRow?) -> some View {
        Group {
            if let dot = row?.dot {
                Circle().fill(dot).frame(width: 9, height: 9)
            } else {
                Image(systemName: row?.symbol ?? "questionmark")
                    .font(.system(size: 15))
                    .foregroundStyle(WTheme.inkSecondary)
            }
        }
        .frame(width: 22)
    }
}
