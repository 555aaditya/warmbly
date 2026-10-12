import SwiftUI

// MARK: - Definitions

/// The drawer's rows and sections, matching the web's scope rail key for key
/// so a layout saved on one shows the same on the other.
enum UniboxRail {
    static let sections = ["favorites", "mail", "views", "mailboxes", "labels", "tags"]

    static func sectionTitle(_ id: String) -> String {
        switch id {
        case "favorites": "Favorites"
        case "mail": "Mail"
        case "views": "Views"
        case "mailboxes": "Mailboxes"
        case "labels": "Labels"
        default: "Tags"
        }
    }

    static let mailKeys = [
        "all", "folder:inbox", "unread", "awaiting", "agent_drafts", "snoozed",
        "folder:drafts", "folder:sent", "scheduled", "folder:archive", "folder:spam", "folder:trash",
    ]

    static func folderTitle(_ folder: String) -> String {
        switch folder {
        case "inbox": "Inbox"
        case "drafts": "Drafts"
        case "sent": "Sent"
        case "archive": "Archive"
        case "spam": "Spam"
        case "trash": "Trash"
        default: folder.capitalized
        }
    }

    static func folderSymbol(_ folder: String) -> String {
        switch folder {
        case "inbox": "tray"
        case "drafts": "doc"
        case "sent": "paperplane"
        case "archive": "archivebox"
        case "spam": "xmark.octagon"
        default: "trash"
        }
    }

    struct View: Identifiable {
        let id: String
        let label: String
        let symbol: String
        let meaning: String
        let labels: [String]
        var automated = false
    }

    /// Premade views over the automatic labels (`web/src/lib/unibox/views.ts`).
    static let views: [View] = [
        View(id: "action_required", label: "Action required", symbol: "exclamationmark.triangle",
             meaning: "Automated mail that needs someone to act: a failed payment, a suspended account, a suspicious sign-in, a sending limit, a service about to expire.",
             labels: ["action required"]),
        View(id: "hot", label: "Hot leads", symbol: "flame",
             meaning: "Replies that said yes, asked for a meeting, or asked about pricing.",
             labels: ["interested", "meeting", "pricing"]),
        View(id: "needs_reply", label: "Needs a reply", symbol: "text.bubble",
             meaning: "They wrote last and nobody has answered for two days or more.",
             labels: ["needs reply"]),
        View(id: "follow_up", label: "Follow up", symbol: "clock.arrow.circlepath",
             meaning: "You wrote last and heard nothing, or an interested thread went quiet.",
             labels: ["follow up", "gone quiet"]),
        View(id: "declined", label: "Declined", symbol: "nosign",
             meaning: "Not interested, wrong person, or asked to unsubscribe. Never chased again.",
             labels: ["not interested", "wrong person", "unsubscribe"]),
        View(id: "automated", label: "Automated", symbol: "gearshape.2",
             meaning: "Sign-in codes, receipts, newsletters, bounces and auto-replies. Kept out of the inbox because nobody wrote them.",
             labels: ["bounced", "out of office", "auto-reply", "notification"], automated: true),
    ]

    static var viewKeys: [String] { views.map { "view:\($0.id)" } }

    /// What each automatic label means (`web/src/lib/unibox/tagMeanings.ts`).
    static let labelMeanings: [String: String] = [
        "bounced": "The email was not delivered: the address does not exist, or the server refused it for now.",
        "out of office": "An automatic out-of-office or vacation reply, not a person.",
        "auto-reply": "An automatic \"we got your message\" or ticket receipt, not a person.",
        "notification": "An automatic message from a service: security alerts, sign-in codes, receipts, newsletters.",
        "action required": "An automatic message saying something needs your action. Kept in the inbox so it is not missed.",
        "sales pitch": "Someone trying to sell to you. Not a reply to your outreach.",
        "interested": "They said yes to the call, the partnership, or the next step.",
        "meeting": "They asked for a call or meeting, or proposed or confirmed a time.",
        "pricing": "They asked about price, terms, or commercials.",
        "question": "Open to it, but asking questions before deciding.",
        "update": "News on something already agreed, or the answer to something you asked.",
        "not now": "Interested in principle, but says the timing is wrong.",
        "not interested": "They declined. Never chased again.",
        "wrong person": "Not the right contact, or they named someone else.",
        "unsubscribe": "They asked to stop receiving email, or complained. Never chased again.",
        "legal threat": "They threatened legal action or named a regulator.",
        "needs reply": "They replied and nobody has answered for two days or more.",
        "follow up": "You wrote last, five days ago, and heard nothing back.",
        "gone quiet": "They were interested, then went quiet for ten days. Worth a nudge.",
        "needs review": "The classifier was not sure about this one. Read it yourself.",
    ]

    static let favoriteNameMax = 40

    /// Collapses whitespace and caps at 40 characters, like the server.
    static func cleanFavoriteName(_ raw: String) -> String {
        let collapsed = raw.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        return String(collapsed.prefix(favoriteNameMax))
    }
}

/// One drawer row, resolved from the overview.
struct UniboxRailRow: Identifiable, Equatable {
    let key: String
    let label: String
    var symbol: String?
    var dot: Color?
    var count: Int?
    var accent = false
    var meaning: String?
    var scope: UniboxScope

    var id: String { key }

    /// Folder rows can be marked read as a whole.
    var folder: String? {
        if case let .folder(folder) = scope { return folder }
        return nil
    }
}

// MARK: - Layout

/// `models.UniboxRailLayout`, saved to the account under `unibox_rail`.
struct UniboxRailLayout: Codable, Sendable, Equatable {
    var favorites: [UniboxRailFavorite] = []
    var hidden: [String] = []
    var order: [String: [String]] = [:]
    var sectionOrder: [String] = []

    enum CodingKeys: String, CodingKey {
        case favorites, hidden, order
        case sectionOrder = "section_order"
    }

    init() {}

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        favorites = try container.decodeIfPresent([UniboxRailFavorite].self, forKey: .favorites) ?? []
        hidden = try container.decodeIfPresent([String].self, forKey: .hidden) ?? []
        order = try container.decodeIfPresent([String: [String]].self, forKey: .order) ?? [:]
        sectionOrder = try container.decodeIfPresent([String].self, forKey: .sectionOrder) ?? []
    }
}

struct UniboxRailFavorite: Codable, Sendable, Equatable, Identifiable {
    var key: String
    var name: String?

    var id: String { key }
}

private struct UniboxViewPrefsEnvelope: Codable, Sendable {
    var preferences: Prefs?

    struct Prefs: Codable, Sendable {
        var layout: UniboxRailLayout?
        var updatedAt: String?

        enum CodingKeys: String, CodingKey {
            case layout
            case updatedAt = "updated_at"
        }
    }
}

private struct UniboxRailSaveBody: Encodable, Sendable {
    var layout: UniboxRailLayout
}

/// The member's rail layout. Favorites, hidden rows and order follow the
/// account (`/v1/me/views/unibox_rail`); folds stay on this device.
@MainActor
@Observable
final class UniboxRailStore {
    private(set) var layout = UniboxRailLayout()
    private(set) var folded: [String: Bool] = [:]
    private var owner = ""
    private var api: APIClient?
    private var saveTask: Task<Void, Never>?

    func load(_ api: APIClient, owner: String) async {
        self.api = api
        if owner != self.owner {
            self.owner = owner
            folded = (UserDefaults.standard.dictionary(forKey: foldKey) as? [String: Bool]) ?? [:]
        }
        guard let envelope: UniboxViewPrefsEnvelope = try? await api.get("me/views/unibox_rail") else { return }
        if let saved = envelope.preferences?.layout {
            layout = saved
        }
    }

    private var foldKey: String { "unibox.rail.folded.\(owner)" }

    // MARK: Folds

    func isFolded(_ section: String) -> Bool { folded[section] ?? false }

    func setFolded(_ section: String, _ value: Bool) {
        folded[section] = value
        UserDefaults.standard.set(folded, forKey: foldKey)
    }

    func foldOthers(than section: String, present: [String]) {
        for id in present { folded[id] = id != section }
        UserDefaults.standard.set(folded, forKey: foldKey)
    }

    func unfoldAll() {
        folded = [:]
        UserDefaults.standard.set(folded, forKey: foldKey)
    }

    // MARK: Sections

    func sectionOrder() -> [String] {
        let saved = layout.sectionOrder.filter { UniboxRail.sections.contains($0) }
        return saved + UniboxRail.sections.filter { !saved.contains($0) }
    }

    func moveSection(_ id: String, by offset: Int, present: [String]) {
        var order = sectionOrder()
        let visible = order.filter { present.contains($0) }
        guard let at = visible.firstIndex(of: id), visible.indices.contains(at + offset),
              let from = order.firstIndex(of: id), let to = order.firstIndex(of: visible[at + offset]) else { return }
        order.swapAt(from, to)
        setSectionOrder(order)
    }

    func setSectionOrder(_ order: [String]) {
        layout.sectionOrder = order == UniboxRail.sections ? [] : order
        scheduleSave()
    }

    // MARK: Rows

    static func defaults(for section: String) -> [String] {
        section == "views" ? UniboxRail.viewKeys : UniboxRail.mailKeys
    }

    func rowOrder(_ section: String) -> [String] {
        let defaults = Self.defaults(for: section)
        let saved = (layout.order[section] ?? []).filter { defaults.contains($0) }
        return saved + defaults.filter { !saved.contains($0) }
    }

    func setRowOrder(_ section: String, _ keys: [String]) {
        if keys == Self.defaults(for: section) {
            layout.order[section] = nil
        } else {
            layout.order[section] = keys
        }
        scheduleSave()
    }

    func moveRow(_ section: String, key: String, by offset: Int, visible: [String]) {
        var order = rowOrder(section)
        guard let at = visible.firstIndex(of: key), visible.indices.contains(at + offset),
              let from = order.firstIndex(of: key), let to = order.firstIndex(of: visible[at + offset]) else { return }
        order.swapAt(from, to)
        setRowOrder(section, order)
    }

    func isHidden(_ key: String) -> Bool { layout.hidden.contains(key) }

    func setHidden(_ key: String, _ hidden: Bool) {
        layout.hidden.removeAll { $0 == key }
        if hidden { layout.hidden.append(key) }
        scheduleSave()
    }

    func hiddenCount(_ section: String) -> Int {
        Self.defaults(for: section).filter(isHidden).count
    }

    func isCustomized(_ section: String) -> Bool {
        layout.order[section] != nil || hiddenCount(section) > 0
    }

    func reset(_ section: String) {
        let keys = Self.defaults(for: section)
        layout.hidden.removeAll { keys.contains($0) }
        layout.order[section] = nil
        scheduleSave()
    }

    // MARK: Favorites

    func isFavorite(_ key: String) -> Bool { layout.favorites.contains { $0.key == key } }

    func toggleFavorite(_ key: String) {
        if isFavorite(key) {
            layout.favorites.removeAll { $0.key == key }
        } else {
            layout.favorites.append(UniboxRailFavorite(key: key))
        }
        scheduleSave()
    }

    /// An empty name, or the row's own label, clears the custom name.
    func rename(_ key: String, to raw: String, original: String) {
        guard let index = layout.favorites.firstIndex(where: { $0.key == key }) else { return }
        let name = UniboxRail.cleanFavoriteName(raw)
        layout.favorites[index].name = name.isEmpty || name == original ? nil : name
        scheduleSave()
    }

    func moveFavorite(_ key: String, by offset: Int, visible: [String]) {
        guard let at = visible.firstIndex(of: key), visible.indices.contains(at + offset),
              let from = layout.favorites.firstIndex(where: { $0.key == key }),
              let to = layout.favorites.firstIndex(where: { $0.key == visible[at + offset] }) else { return }
        layout.favorites.swapAt(from, to)
        scheduleSave()
    }

    func setFavorites(_ favorites: [UniboxRailFavorite]) {
        layout.favorites = favorites
        scheduleSave()
    }

    func clearFavorites() {
        layout.favorites = []
        scheduleSave()
    }

    // MARK: Save

    /// Debounced like the web, so a burst of edits is one write.
    private func scheduleSave() {
        saveTask?.cancel()
        let body = UniboxRailSaveBody(layout: layout)
        saveTask = Task { [api] in
            try? await Task.sleep(for: .milliseconds(600))
            guard !Task.isCancelled, let api else { return }
            let _: UniboxViewPrefsEnvelope? = try? await api.put("me/views/unibox_rail", body: body)
        }
    }
}
