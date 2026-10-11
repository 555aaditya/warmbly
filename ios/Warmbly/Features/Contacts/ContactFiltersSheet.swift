import SwiftUI

/// Advanced criteria sheet for the contacts browser. Flat, inbox-style layout
/// (eyebrow captions + hairline rows, no boxy cards) built on a lazy List so it
/// opens instantly: single-select criteria are compact dropdown menus, ranges
/// use themed steppers, dates reveal a compact picker, custom fields swipe to
/// delete. Edits a local draft and commits on Apply.
struct ContactFiltersSheet: View {
    @Environment(\.dismiss) private var dismiss

    let categories: [ContactCategory]
    let initial: ContactAdvancedFilters
    let onApply: (ContactAdvancedFilters) -> Void

    @State private var draft: ContactAdvancedFilters

    init(
        categories: [ContactCategory],
        initial: ContactAdvancedFilters,
        onApply: @escaping (ContactAdvancedFilters) -> Void
    ) {
        self.categories = categories
        self.initial = initial
        self.onApply = onApply
        _draft = State(initialValue: initial)
    }

    var body: some View {
        NavigationStack {
            List {
                eyebrow("Sort")
                item {
                    menuRow(
                        "Sort by",
                        value: draft.sortBy.label,
                        options: ContactSort.allCases.map { ($0.label, draft.sortBy == $0) }
                    ) { picked in
                        if let sort = ContactSort.allCases.first(where: { $0.label == picked }) { draft.sortBy = sort }
                    }
                }
                item {
                    let order = draft.sortBy.orderLabels
                    menuRow(
                        "Order",
                        value: draft.reverse ? order.reversed : order.natural,
                        options: [(order.natural, !draft.reverse), (order.reversed, draft.reverse)]
                    ) { picked in draft.reverse = (picked == order.reversed) }
                }

                eyebrow("Filter")
                item {
                    menuRow(
                        "Subscription",
                        value: subLabel,
                        options: [("Any", draft.subscribed == nil), ("Subscribed", draft.subscribed == true), ("Unsubscribed", draft.subscribed == false)]
                    ) { picked in
                        switch picked {
                        case "Subscribed": draft.subscribed = true
                        case "Unsubscribed": draft.subscribed = false
                        default: draft.subscribed = nil
                        }
                    }
                }
                if !categories.isEmpty {
                    item { categoriesRow }
                }
                item { rangeRow("In at least", value: $draft.minCampaigns) }
                item { rangeRow("In at most", value: $draft.maxCampaigns) }

                eyebrow("Dates")
                item { dateRow("Created after", value: $draft.createdAfter) }
                item { dateRow("Created before", value: $draft.createdBefore) }
                item { dateRow("Updated after", value: $draft.updatedAfter) }
                item { dateRow("Updated before", value: $draft.updatedBefore) }

                eyebrow("Custom fields")
                ForEach($draft.customFields) { $field in
                    customFieldRow($field)
                        .listRowInsets(EdgeInsets(top: 10, leading: 20, bottom: 10, trailing: 16))
                        .listRowBackground(WTheme.canvas)
                }
                .onDelete { draft.customFields.remove(atOffsets: $0) }
                item {
                    Button {
                        withAnimation(.snappy) { draft.customFields.append(ContactFieldFilter()) }
                    } label: {
                        Label("Add custom field", systemImage: "plus")
                            .font(.system(size: 13.5, weight: .medium))
                    }
                    .buttonStyle(OutlineButtonStyle(size: .small))
                }
                Text("Match by any custom property, like a job title or plan.")
                    .font(.system(size: 12.5))
                    .foregroundStyle(WTheme.inkTertiary)
                    .listRowInsets(EdgeInsets(top: 4, leading: 20, bottom: 24, trailing: 20))
                    .listRowSeparator(.hidden)
                    .listRowBackground(WTheme.canvas)
            }
            .listStyle(.plain)
            .listRowSeparatorTint(WTheme.hairline)
            .scrollContentBackground(.hidden)
            .background(WTheme.canvas)
            .scrollDismissesKeyboard(.interactively)
            .navigationTitle("Filters")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Apply") { onApply(draft); dismiss() }.fontWeight(.semibold)
                }
                ToolbarItem(placement: .bottomBar) {
                    Button(role: .destructive) {
                        withAnimation(.snappy) { draft = ContactAdvancedFilters() }
                    } label: {
                        Label("Reset all filters", systemImage: "arrow.counterclockwise")
                    }
                    .disabled(!draft.isActive)
                }
            }
        }
        .presentationDetents([.large])
        .presentationDragIndicator(.visible)
    }

    private var subLabel: String {
        switch draft.subscribed {
        case .some(true): "Subscribed"
        case .some(false): "Unsubscribed"
        case nil: "Any"
        }
    }

    // MARK: List primitives

    private func eyebrow(_ title: String) -> some View {
        EyebrowLabel(title)
            .listRowInsets(EdgeInsets(top: 22, leading: 20, bottom: 6, trailing: 20))
            .listRowSeparator(.hidden)
            .listRowBackground(WTheme.canvas)
    }

    private func item<V: View>(@ViewBuilder _ content: () -> V) -> some View {
        content()
            .frame(maxWidth: .infinity, alignment: .leading)
            .listRowInsets(EdgeInsets(top: 6, leading: 20, bottom: 6, trailing: 20))
            .listRowBackground(WTheme.canvas)
    }

    // MARK: Rows

    /// A row whose value is a native dropdown menu (label + value + chevrons).
    private func menuRow(_ label: String, value: String, options: [(String, Bool)], onPick: @escaping (String) -> Void) -> some View {
        HStack {
            Text(label).font(.system(size: 15)).foregroundStyle(WTheme.ink)
            Spacer(minLength: 8)
            Menu {
                ForEach(options, id: \.0) { title, selected in
                    Button { onPick(title) } label: {
                        if selected { Label(title, systemImage: "checkmark") } else { Text(title) }
                    }
                }
            } label: {
                FilterValueChip(text: value)
            }
        }
        .frame(minHeight: 34)
    }

    /// Category multi-select as a dropdown of checkable items.
    private var categoriesRow: some View {
        HStack {
            Text("Categories").font(.system(size: 15)).foregroundStyle(WTheme.ink)
            Spacer(minLength: 8)
            Menu {
                if !draft.categoryIDs.isEmpty {
                    Button(role: .destructive) { draft.categoryIDs.removeAll() } label: { Label("Clear", systemImage: "xmark") }
                }
                ForEach(categories) { category in
                    Button {
                        if draft.categoryIDs.contains(category.id) { draft.categoryIDs.remove(category.id) }
                        else { draft.categoryIDs.insert(category.id) }
                    } label: {
                        if draft.categoryIDs.contains(category.id) {
                            Label(category.title ?? "Untitled", systemImage: "checkmark")
                        } else {
                            Text(category.title ?? "Untitled")
                        }
                    }
                }
            } label: {
                FilterValueChip(text: draft.categoryIDs.isEmpty ? "All" : "\(draft.categoryIDs.count) selected")
            }
        }
        .frame(minHeight: 34)
    }

    private func rangeRow(_ label: String, value: Binding<Int?>) -> some View {
        let enabled = value.wrappedValue != nil
        return HStack(spacing: 12) {
            Text(label).font(.system(size: 15)).foregroundStyle(enabled ? WTheme.ink : WTheme.inkSecondary)
            Spacer(minLength: 8)
            if enabled {
                MiniStepper(value: Binding(get: { value.wrappedValue ?? 0 }, set: { value.wrappedValue = $0 }), range: 0 ... 100)
                    .transition(.scale.combined(with: .opacity))
                Text("campaign\((value.wrappedValue ?? 0) == 1 ? "" : "s")").font(WFont.meta(11)).foregroundStyle(WTheme.inkTertiary)
            }
            Toggle("", isOn: Binding(
                get: { enabled },
                set: { on in withAnimation(.spring(response: 0.35, dampingFraction: 0.82)) { value.wrappedValue = on ? (value.wrappedValue ?? 1) : nil } }
            ))
            .labelsHidden()
        }
        .frame(minHeight: 34)
    }

    private func dateRow(_ label: String, value: Binding<Date?>) -> some View {
        let enabled = value.wrappedValue != nil
        return HStack(spacing: 12) {
            Text(label).font(.system(size: 15)).foregroundStyle(enabled ? WTheme.ink : WTheme.inkSecondary)
            Spacer(minLength: 8)
            if enabled {
                DatePicker("", selection: Binding(get: { value.wrappedValue ?? Date() }, set: { value.wrappedValue = $0 }), displayedComponents: .date)
                    .labelsHidden()
                    .transition(.opacity)
            }
            Toggle("", isOn: Binding(
                get: { enabled },
                set: { on in withAnimation(.spring(response: 0.35, dampingFraction: 0.82)) { value.wrappedValue = on ? (value.wrappedValue ?? Date()) : nil } }
            ))
            .labelsHidden()
        }
        .frame(minHeight: 34)
    }

    /// A clean two-line custom-field row: name + match-type dropdown on top,
    /// a plain value field below. Swipe the row to delete it.
    private func customFieldRow(_ field: Binding<ContactFieldFilter>) -> some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 10) {
                TextField("Field name", text: field.name)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(WTheme.ink)
                Menu {
                    ForEach(ContactFilterType.allCases, id: \.self) { type in
                        Button { field.wrappedValue.type = type } label: {
                            if field.wrappedValue.type == type { Label(type.label, systemImage: "checkmark") } else { Text(type.label) }
                        }
                    }
                } label: {
                    FilterValueChip(text: field.wrappedValue.type.label)
                }
            }
            TextField("Value to match", text: field.value)
                .font(.system(size: 14))
                .foregroundStyle(WTheme.inkSecondary)
        }
    }
}

/// Hairline value chip with up/down chevrons, the label of a dropdown menu.
private struct FilterValueChip: View {
    let text: String

    var body: some View {
        HStack(spacing: 5) {
            Text(text)
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(WTheme.ink)
                .contentTransition(.numericText())
                .lineLimit(1)
            Image(systemName: "chevron.up.chevron.down")
                .font(.system(size: 9, weight: .medium))
                .foregroundStyle(WTheme.inkTertiary)
        }
        .padding(.horizontal, 10)
        .frame(height: 28)
        .background(WTheme.raised, in: Capsule())
        .overlay(Capsule().strokeBorder(WTheme.hairlineStrong, lineWidth: 1))
    }
}

/// Two round hairline steppers around a monospaced value.
private struct MiniStepper: View {
    @Binding var value: Int
    let range: ClosedRange<Int>

    var body: some View {
        HStack(spacing: 8) {
            button("minus") { if value > range.lowerBound { value -= 1 } }
            Text("\(value)")
                .font(WFont.meta(14, weight: .medium))
                .foregroundStyle(WTheme.ink)
                .monospacedDigit()
                .frame(minWidth: 22)
                .contentTransition(.numericText())
            button("plus") { if value < range.upperBound { value += 1 } }
        }
    }

    private func button(_ symbol: String, action: @escaping () -> Void) -> some View {
        Button {
            withAnimation(.snappy) { action() }
        } label: {
            Image(systemName: symbol)
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(WTheme.ink)
                .frame(width: 28, height: 28)
                .background(WTheme.raised, in: Circle())
                .overlay(Circle().strokeBorder(WTheme.hairlineStrong, lineWidth: 1))
        }
        .buttonStyle(TapScaleStyle())
    }
}
