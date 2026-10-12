import SwiftUI

/// One contact in the browser: who they are and where they work, their labels,
/// and on the right the last thing that happened with them. Engagement in the
/// last week is coloured and rings the avatar; anything older reads grey. A new
/// event arriving live washes the row in its colour for a moment.
struct ContactRow: View {
    let contact: Contact
    var selecting = false
    var selected = false

    @State private var flash = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var latest: (kind: ContactOutreach.Kind, at: Date)? { contact.outreach?.latest }

    private var isWarm: Bool {
        guard let latest, latest.kind.isEngagement else { return false }
        return Date.now.timeIntervalSince(latest.at) < 7 * 86_400
    }

    /// Only a reply or a click rings the avatar; an open is too common to.
    private var isHot: Bool { isWarm && (latest?.kind == .replied || latest?.kind == .clicked) }

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            if selecting {
                ContactCheckCircle(on: selected)
                    .padding(.top, 11)
                    .transition(.scale.combined(with: .opacity))
            }
            avatar
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(contact.displayName)
                        .font(.system(size: 16, weight: .semibold))
                        .tracking(-0.2)
                        .foregroundStyle(WTheme.ink)
                        .lineLimit(1)
                    if contact.subscribed == false {
                        Image(systemName: "bell.slash.fill")
                            .font(.system(size: 11))
                            .foregroundStyle(WTheme.inkTertiary)
                            .accessibilityLabel("Unsubscribed")
                    }
                    Spacer(minLength: 8)
                    trailing
                }
                secondaryLine
                if let categories = contact.categories, !categories.isEmpty {
                    chips(categories)
                        .padding(.top, 3)
                }
            }
        }
        .padding(.vertical, 8)
        .background {
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .fill((latest?.kind.color ?? WTheme.accent).opacity(flash ? 0.12 : 0))
                .padding(.horizontal, -8)
        }
        .contentShape(Rectangle())
        .onChange(of: latest?.at) { old, new in
            guard let old, let new, new > old, !reduceMotion else { return }
            withAnimation(.easeOut(duration: 0.25)) { flash = true }
            withAnimation(.easeOut(duration: 1.4).delay(0.6)) { flash = false }
        }
        .accessibilityElement(children: .combine)
    }

    // MARK: Pieces

    private var avatar: some View {
        WAvatar(name: contact.displayName, seed: contact.id, size: 44)
            .padding(3)
            .overlay {
                if isHot, let latest {
                    Circle()
                        .strokeBorder(latest.kind.color, lineWidth: 2)
                        .transition(.scale(scale: 0.8).combined(with: .opacity))
                }
            }
            .animation(.spring(response: 0.5, dampingFraction: 0.7), value: isHot)
            .padding(-3)
    }

    /// Company first, then the address; a row titled by its address shows the
    /// company alone.
    @ViewBuilder
    private var secondaryLine: some View {
        let company = contact.company?.trimmingCharacters(in: .whitespaces) ?? ""
        let email = contact.hasName ? (contact.email ?? "") : ""
        if !company.isEmpty || !email.isEmpty {
            Group {
                if !company.isEmpty, !email.isEmpty {
                    Text("\(Text(company).foregroundStyle(WTheme.ink.opacity(0.75)).fontWeight(.medium)) · \(email)")
                } else {
                    Text(company.isEmpty ? email : company)
                }
            }
            .font(.system(size: 13.5))
            .foregroundStyle(WTheme.inkSecondary)
            .lineLimit(1)
        }
    }

    @ViewBuilder
    private var trailing: some View {
        if let lead = contact.campaignLead {
            StatusPill(
                text: ContactLeadStatus.label(lead.status),
                tone: ContactLeadStatus.tone(lead.status),
                pulsing: ContactLeadStatus.isLive(lead.status)
            )
        } else if let latest {
            let tint = isWarm || latest.kind == .bounced ? latest.kind.color : WTheme.inkTertiary
            HStack(spacing: 4) {
                Image(systemName: latest.kind.symbol)
                    .font(.system(size: 10, weight: .semibold))
                Text("\(latest.kind.label) \(WFormat.ago(latest.at))")
                    .font(.system(size: 12.5, weight: isWarm ? .semibold : .regular))
                    .monospacedDigit()
            }
            .foregroundStyle(tint)
            .lineLimit(1)
            .fixedSize()
            .contentTransition(.opacity)
            .animation(.snappy, value: latest.at)
        }
    }

    /// Quiet label chips, the same as the inbox's: the colour stays a dot.
    private func chips(_ categories: [ContactMiniCategory]) -> some View {
        HStack(spacing: 4) {
            ForEach(categories.prefix(3)) { category in
                HStack(spacing: 4) {
                    Circle()
                        .fill(ContactColor.dot(category.color, seed: category.id))
                        .frame(width: 6, height: 6)
                    Text(category.title ?? "")
                        .font(.system(size: 11.5, weight: .medium))
                        .foregroundStyle(WTheme.inkSecondary)
                        .lineLimit(1)
                }
                .padding(.horizontal, 7)
                .frame(height: 20)
                .background(WTheme.raised, in: Capsule())
                .overlay(Capsule().strokeBorder(WTheme.hairlineStrong, lineWidth: 1))
            }
            if categories.count > 3 {
                Text("+\(categories.count - 3)")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(WTheme.inkTertiary)
            }
        }
    }
}

// MARK: - Sections

/// A run of loaded contacts sharing a heading under the current sort: a letter
/// for names and addresses, a period for dates, no heading otherwise.
struct ContactSection: Identifiable {
    let id: String
    let title: String?
    /// Position of the first contact in the whole list, for the entrance stagger.
    let start: Int
    let contacts: [Contact]

    static func build(_ contacts: [Contact], sort: ContactSort, now: Date = .now) -> [ContactSection] {
        var sections: [ContactSection] = []
        var current: (key: String, start: Int, rows: [Contact])?
        for (index, contact) in contacts.enumerated() {
            let key = heading(contact, sort: sort, now: now) ?? ""
            if let open = current, open.key == key {
                current?.rows.append(contact)
            } else {
                if let open = current {
                    sections.append(ContactSection(id: "\(open.key)#\(open.start)", title: open.key.isEmpty ? nil : open.key, start: open.start, contacts: open.rows))
                }
                current = (key, index, [contact])
            }
        }
        if let open = current {
            sections.append(ContactSection(id: "\(open.key)#\(open.start)", title: open.key.isEmpty ? nil : open.key, start: open.start, contacts: open.rows))
        }
        return sections
    }

    private static func heading(_ contact: Contact, sort: ContactSort, now: Date) -> String? {
        switch sort {
        case .firstName: letter(contact.firstName ?? contact.displayName)
        case .lastName: letter(contact.lastName ?? contact.displayName)
        case .email: letter(contact.email ?? "")
        case .createdAt: contact.createdAt.map { period($0, now: now) }
        case .updatedAt: contact.updatedAt.map { period($0, now: now) }
        case .campaignCount: nil
        }
    }

    private static func letter(_ raw: String) -> String {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let first = trimmed.first else { return "#" }
        let folded = String(first).folding(options: [.diacriticInsensitive, .caseInsensitive], locale: nil).uppercased()
        return folded.first?.isLetter == true ? folded : "#"
    }

    private static func period(_ date: Date, now: Date) -> String {
        let calendar = Calendar.current
        if calendar.isDate(date, inSameDayAs: now) { return "Today" }
        if calendar.isDateInYesterday(date) { return "Yesterday" }
        if let week = calendar.date(byAdding: .day, value: -7, to: now), date > week { return "This week" }
        if calendar.isDate(date, equalTo: now, toGranularity: .month) { return "Earlier this month" }
        if calendar.isDate(date, equalTo: now, toGranularity: .year) {
            return date.formatted(.dateTime.month(.wide))
        }
        return date.formatted(.dateTime.month(.wide).year())
    }
}
