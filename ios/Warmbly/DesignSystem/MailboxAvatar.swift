import SwiftUI

/// Who runs a mailbox, as the app names and draws it: the inbox vendor it was
/// imported from, else its detected mail host, else how it was connected.
/// Mirrors `web/src/lib/brands.ts` and `mailboxSource.ts`.
struct MailboxBrand: Hashable, Sendable {
    enum Mark: Hashable, Sendable {
        case google, microsoft, asset(String), server
    }

    let mark: Mark
    /// "Google Workspace", "Zoho Mail", "via Zapmail", "SMTP/IMAP".
    let label: String

    init(provider: String?, mailHost: String?, authMethod: String? = nil, vendor: String? = nil) {
        let host = (mailHost ?? "").lowercased()
        let vendor = (vendor ?? "").lowercased()
        if !vendor.isEmpty {
            mark = Self.vendorLabels[vendor] != nil ? .asset("Vendor-\(vendor)") : Self.mark(host: host, provider: provider)
            label = "via \(Self.vendorLabels[vendor] ?? vendor)"
            return
        }
        mark = Self.mark(host: host, provider: provider)
        if authMethod == "delegated" {
            label = mark == .microsoft ? "Microsoft 365" : "Google Workspace"
        } else if let name = Self.hostLabels[host] {
            label = name
        } else {
            label = switch provider {
            case "gmail": "Gmail"
            case "outlook": "Outlook"
            case "smtp_imap", nil, "": "SMTP/IMAP"
            default: provider ?? "Mailbox"
            }
        }
    }

    private static func mark(host: String, provider: String?) -> Mark {
        switch host {
        case "google_workspace", "gmail": return .google
        case "microsoft365", "outlook": return .microsoft
        default:
            if hostAssets.contains(host) { return .asset("Host-\(host)") }
        }
        switch provider {
        case "gmail": return .google
        case "outlook": return .microsoft
        default: return .server
        }
    }

    private static let hostAssets: Set<String> = [
        "aol", "fastmail", "gmx", "godaddy", "hostinger", "icloud", "ionos", "migadu",
        "namecheap", "ovh", "proton", "purelymail", "rackspace", "yahoo", "yandex", "zoho",
    ]

    private static let hostLabels: [String: String] = [
        "google_workspace": "Google Workspace", "gmail": "Gmail",
        "microsoft365": "Microsoft 365", "outlook": "Outlook.com",
        "zoho": "Zoho Mail", "yahoo": "Yahoo Mail", "aol": "AOL Mail", "icloud": "iCloud Mail",
        "fastmail": "Fastmail", "godaddy": "GoDaddy", "namecheap": "Namecheap", "ionos": "IONOS",
        "hostinger": "Hostinger", "ovh": "OVHcloud", "migadu": "Migadu", "purelymail": "Purelymail",
        "rackspace": "Rackspace", "yandex": "Yandex Mail", "gmx": "GMX", "proton": "Proton Mail",
    ]

    private static let vendorLabels: [String: String] = [
        "inboxkit": "InboxKit", "zapmail": "Zapmail", "mailforge": "Mailforge", "infraforge": "Infraforge",
        "maildoso": "Maildoso", "cheapinboxes": "Cheap Inboxes", "scaledmail": "ScaledMail",
    ]
}

/// A service's logo, drawn without a frame.
struct ProviderMark: View {
    let mark: MailboxBrand.Mark
    var size: CGFloat = 16

    var body: some View {
        Group {
            switch mark {
            case .google:
                Image("GoogleLogo").resizable().scaledToFit()
            case .microsoft:
                Image("OutlookLogo").resizable().scaledToFit()
            case let .asset(name):
                Image(name).resizable().scaledToFit()
                    .clipShape(RoundedRectangle(cornerRadius: size * 0.18, style: .continuous))
            case .server:
                Image(systemName: "server.rack")
                    .font(.system(size: size * 0.78, weight: .medium))
                    .foregroundStyle(WTheme.inkSecondary)
            }
        }
        .frame(width: size, height: size)
        .accessibilityHidden(true)
    }
}

/// A mailbox's face: its profile photo (initials until one exists) with the
/// service that runs it riding the corner.
struct MailboxAvatar: View {
    let email: String
    var avatarURL: String?
    let brand: MailboxBrand
    var seed: String = ""
    var size: CGFloat = 44
    /// Matches the badge's ring to whatever the avatar sits on.
    var ring: Color = WTheme.groupedCard

    var body: some View {
        let badge = max(17, size * 0.44)
        WAvatar(
            name: WFormat.mailboxName(email),
            imageURL: avatarURL?.isEmpty == false ? avatarURL : nil,
            seed: seed.isEmpty ? email : seed,
            size: size
        )
        .overlay(alignment: .bottomTrailing) {
            ProviderMark(mark: brand.mark, size: badge * 0.62)
                .frame(width: badge, height: badge)
                .background(.white, in: Circle())
                .overlay(Circle().strokeBorder(Color.black.opacity(0.06), lineWidth: 0.5))
                .padding(1.5)
                .background(ring, in: Circle())
                .offset(x: size * 0.16, y: size * 0.12)
        }
        .accessibilityLabel("\(email), \(brand.label)")
    }
}
