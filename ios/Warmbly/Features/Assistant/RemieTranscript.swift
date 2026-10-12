import SwiftUI

// The pieces of a Remie conversation, ported from the web agent panel
// (AgentPanel, AgentActivity, Markdown) so both read the same.

// MARK: - Tool labels

/// Human labels for tool calls in two tenses, derived from the tool name like
/// the web's toolLabels.ts.
enum RemieTool {
    private static let verbs: [String: (String, String)] = [
        "add": ("Adding", "Added"), "bulk": ("Editing", "Edited"),
        "cancel": ("Canceling", "Canceled"), "complete": ("Completing", "Completed"),
        "compose": ("Composing", "Composed"), "create": ("Creating", "Created"),
        "delete": ("Deleting", "Deleted"), "disconnect": ("Disconnecting", "Disconnected"),
        "draft": ("Drafting", "Drafted"), "fetch": ("Fetching", "Fetched"),
        "get": ("Reading", "Read"), "invite": ("Inviting", "Invited"),
        "list": ("Listing", "Listed"), "load": ("Loading", "Loaded"),
        "mark": ("Marking", "Marked"), "mint": ("Creating", "Created"),
        "move": ("Moving", "Moved"), "preview": ("Previewing", "Previewed"),
        "remove": ("Removing", "Removed"), "revoke": ("Revoking", "Revoked"),
        "rotate": ("Rotating", "Rotated"), "run": ("Running", "Ran"),
        "search": ("Searching", "Searched"), "send": ("Sending", "Sent"),
        "set": ("Updating", "Updated"), "snooze": ("Snoozing", "Snoozed"),
        "submit": ("Submitting", "Submitted"), "unsnooze": ("Unsnoozing", "Unsnoozed"),
        "update": ("Updating", "Updated"), "verify": ("Verifying", "Verified"),
    ]

    private static let named: [String: (String, String)] = [
        "search_web": ("Searching the web", "Searched the web"),
        "fetch_url": ("Reading a web page", "Read a web page"),
        "load_skill": ("Loading a skill", "Loaded a skill"),
        "get_thread": ("Reading the thread", "Read the thread"),
        "list_threads": ("Scanning the inbox", "Scanned the inbox"),
        "get_dashboard_analytics": ("Reading analytics", "Read analytics"),
        "get_advisor_summary": ("Checking the advisor", "Checked the advisor"),
        "bulk_edit_contacts": ("Editing contacts", "Edited contacts"),
        "add_tag": ("Adding a label", "Added a label"),
        "remove_tag": ("Removing a label", "Removed a label"),
        "set_thread_labels": ("Labeling the thread", "Labeled the thread"),
    ]

    static func label(_ tool: String?, done: Bool = true) -> String {
        guard let tool, !tool.isEmpty else { return done ? "Ran a step" : "Working" }
        if let n = named[tool] { return done ? n.1 : n.0 }
        let parts = tool.split(separator: "_").map(String.init)
        let object = parts.dropFirst().joined(separator: " ")
        guard let first = parts.first, let v = verbs[first] else { return action(tool) }
        let verb = done ? v.1 : v.0
        return object.isEmpty ? verb : "\(verb) \(object)"
    }

    /// The imperative form an approval asks about ("Update contact fields").
    static func action(_ tool: String?) -> String {
        let plain = (tool ?? "").replacingOccurrences(of: "_", with: " ")
        guard let first = plain.first else { return "Run a tool" }
        return first.uppercased() + plain.dropFirst()
    }
}

// MARK: - Markdown

/// The web's small Markdown: paragraphs, lists, headings and code fences,
/// with inline emphasis, code and links.
struct RemieMarkdown: View {
    let text: String
    /// Puts the insertion caret after the last paragraph.
    var caret = false
    /// Text is still being revealed: the caret holds and the newest characters fade in.
    var typing = false

    private enum Block {
        case para(String)
        case heading(String)
        case list(ordered: Bool, items: [String])
        case code(String)
    }

    var body: some View {
        let blocks = Self.parse(text)
        VStack(alignment: .leading, spacing: 9) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { index, block in
                view(block, isLast: index == blocks.count - 1)
            }
        }
        .font(.system(size: 16))
        .lineSpacing(5)
        .foregroundStyle(WTheme.ink)
        .tint(Remie.sky500)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder
    private func view(_ block: Block, isLast: Bool) -> some View {
        switch block {
        case let .para(s):
            paragraph(s, bold: false, isLast: isLast)
        case let .heading(s):
            paragraph(s, bold: true, isLast: isLast)
        case let .list(ordered, items):
            VStack(alignment: .leading, spacing: 5) {
                ForEach(Array(items.enumerated()), id: \.offset) { i, item in
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Text(ordered ? "\(i + 1)." : "•")
                            .foregroundStyle(WTheme.inkTertiary)
                            .monospacedDigit()
                        Text(Self.inline(item))
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
            .padding(.leading, 2)
        case let .code(s):
            ScrollView(.horizontal, showsIndicators: false) {
                Text(s)
                    .font(WFont.code(12.5))
                    .lineSpacing(3)
                    .foregroundStyle(WTheme.ink.opacity(0.85))
                    .padding(.horizontal, 11)
                    .padding(.vertical, 9)
            }
            .background(WTheme.surface, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(WTheme.hairlineStrong, lineWidth: 1))
        }
    }

    @ViewBuilder
    private func paragraph(_ s: String, bold: Bool, isLast: Bool) -> some View {
        var attr = Self.inline(s)
        let _ = Self.fadeTail(&attr, when: typing && isLast)
        if caret && isLast {
            if typing {
                Text("\(Text(attr))\(Self.caret(visible: true))")
                    .fontWeight(bold ? .semibold : nil)
            } else {
                TimelineView(.periodic(from: .now, by: 0.5)) { context in
                    let on = Int(context.date.timeIntervalSinceReferenceDate * 2) % 2 == 0
                    Text("\(Text(attr))\(Self.caret(visible: on))")
                        .fontWeight(bold ? .semibold : nil)
                }
            }
        } else {
            Text(attr)
                .fontWeight(bold ? .semibold : nil)
                .textSelection(.enabled)
        }
    }

    private static func caret(visible: Bool) -> Text {
        Text("\u{258F}").foregroundStyle(visible ? Remie.sky500 : .clear)
    }

    /// The last few characters of still-typing text resolve in rather than pop.
    private static func fadeTail(_ attr: inout AttributedString, when active: Bool) {
        guard active else { return }
        let count = attr.characters.count
        guard count > 0 else { return }
        let n = min(6, count)
        for k in 0 ..< n {
            let start = attr.characters.index(attr.endIndex, offsetBy: -(n - k))
            let end = attr.characters.index(after: start)
            attr[start ..< end].foregroundColor = WTheme.ink.opacity(0.25 + 0.6 * Double(k) / Double(n))
        }
    }

    private static func inline(_ s: String) -> AttributedString {
        var attr = (try? AttributedString(
            markdown: s,
            options: AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        )) ?? AttributedString(s)
        // Addresses the parser auto-links read as text, not as mail links.
        for run in attr.runs where run.link?.scheme == "mailto" {
            attr[run.range].link = nil
        }
        return attr
    }

    private static func parse(_ text: String) -> [Block] {
        var out: [Block] = []
        let parts = text.components(separatedBy: "```")
        for (pi, part) in parts.enumerated() {
            if pi % 2 == 1 {
                // Drop the fence's language line.
                var body = part
                if let nl = body.firstIndex(of: "\n") { body = String(body[body.index(after: nl)...]) }
                let trimmed = body.trimmingCharacters(in: .newlines)
                if !trimmed.isEmpty { out.append(.code(trimmed)) }
                continue
            }
            var para: [String] = []
            var list: (ordered: Bool, items: [String])?
            func flushPara() {
                if !para.isEmpty { out.append(.para(para.joined(separator: "\n"))) }
                para = []
            }
            func flushList() {
                if let l = list { out.append(.list(ordered: l.ordered, items: l.items)) }
                list = nil
            }
            for line in part.components(separatedBy: "\n") {
                let trimmed = line.trimmingCharacters(in: .whitespaces)
                if let item = bullet(trimmed) {
                    flushPara()
                    if list?.ordered != item.ordered { flushList(); list = (item.ordered, []) }
                    list?.items.append(item.text)
                } else if trimmed.hasPrefix("#") {
                    let heading = trimmed.drop { $0 == "#" }.trimmingCharacters(in: .whitespaces)
                    flushPara(); flushList()
                    if !heading.isEmpty { out.append(.heading(heading)) }
                } else if trimmed.isEmpty {
                    flushPara(); flushList()
                } else {
                    flushList()
                    para.append(line)
                }
            }
            flushPara(); flushList()
        }
        return out
    }

    private static func bullet(_ line: String) -> (ordered: Bool, text: String)? {
        if line.hasPrefix("- ") || line.hasPrefix("* ") {
            return (false, String(line.dropFirst(2)))
        }
        let digits = line.prefix { $0.isNumber }
        guard !digits.isEmpty, digits.count <= 3 else { return nil }
        let rest = line.dropFirst(digits.count)
        guard let mark = rest.first, mark == "." || mark == ")", rest.dropFirst().first == " " else { return nil }
        return (true, String(rest.dropFirst(2)))
    }
}

/// Streamed text as a fast typewriter: each frame reveals a twelfth of what
/// is still hidden, so bursts type out at a steady pace.
struct RemieSmoothText: View {
    let text: String
    @State private var shown = 0

    var body: some View {
        let total = text.count
        RemieMarkdown(text: String(text.prefix(shown)), caret: true, typing: shown < total)
            .task(id: text) {
                while !Task.isCancelled {
                    let total = text.count
                    guard shown < total else { break }
                    shown = min(total, shown + max(1, Int((Double(total - shown) / 12).rounded(.up))))
                    try? await Task.sleep(for: .milliseconds(16))
                }
            }
    }
}

// MARK: - Activity

/// A run's tool calls folded into one trace: the step running now while it
/// works, "Ran N steps" after, and every step under a rail when open.
struct RemieActivityTrace: View {
    let steps: [AssistantChatStore.Block]
    let live: Bool
    let streaming: Bool

    @State private var userOpen: Bool?

    private var running: Bool { streaming && steps.contains { !$0.done } }
    private var multi: Bool { steps.count > 1 }
    private var open: Bool { !multi || (userOpen ?? (running || live)) }

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            if multi { header }
            if open {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(steps) { step in
                        RemieStepLine(step: step, streaming: streaming)
                    }
                }
                .padding(.leading, multi ? 15 : 0)
                .overlay(alignment: .leading) {
                    if multi {
                        Rectangle()
                            .fill(WTheme.hairlineStrong)
                            .frame(width: 1)
                            .padding(.top, 4)
                            .padding(.bottom, 8)
                            .padding(.leading, 6.5)
                    }
                }
                .transition(.asymmetric(
                    insertion: .opacity.combined(with: .offset(y: -4)),
                    removal: .opacity
                ))
            }
        }
        .animation(.spring(response: 0.36, dampingFraction: 0.9), value: open)
    }

    private var header: some View {
        let current = steps.first { !$0.done }
        let doneCount = steps.filter(\.done).count
        let summary = steps.reduce(into: [String]()) { names, step in
            let name = RemieTool.label(step.tool)
            if !names.contains(name) { names.append(name) }
        }.joined(separator: ", ")
        return Button {
            userOpen = !open
        } label: {
            HStack(spacing: 8) {
                RemieSpark(size: 14, state: running ? .thinking : .idle, muted: !running)
                if running, let current {
                    Text(RemieTool.label(current.tool, done: false))
                        .font(.system(size: 13.5, weight: .medium))
                        .remieShimmer()
                        .lineLimit(1)
                        .layoutPriority(1)
                    Text("\(doneCount)/\(steps.count)")
                        .font(WFont.meta(11.5))
                        .foregroundStyle(WTheme.inkTertiary)
                        .contentTransition(.numericText())
                } else {
                    Text("Ran \(steps.count) steps")
                        .font(.system(size: 13.5, weight: .medium))
                        .foregroundStyle(WTheme.inkSecondary)
                        .layoutPriority(1)
                    if !open {
                        Text(summary)
                            .font(.system(size: 12.5))
                            .foregroundStyle(WTheme.inkTertiary)
                            .lineLimit(1)
                    }
                }
                Image(systemName: "chevron.down")
                    .font(.system(size: 10.5, weight: .semibold))
                    .foregroundStyle(WTheme.inkTertiary)
                    .rotationEffect(.degrees(open ? 180 : 0))
            }
            .frame(minHeight: 30)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityValue(open ? "Expanded" : "Collapsed")
    }
}

private struct RemieStepLine: View {
    let step: AssistantChatStore.Block
    let streaming: Bool

    var body: some View {
        let detail = [step.result, step.argsSummary].compactMap { $0 }.first { !$0.isEmpty }
        HStack(spacing: 8) {
            status
                .frame(width: 14, height: 14)
            Text(RemieTool.label(step.tool, done: step.done || !streaming))
                .font(.system(size: 13.5, weight: .medium))
                .lineLimit(1)
                .layoutPriority(1)
                .modifier(LabelStyle(active: !step.done && streaming))
            if let detail {
                Text(detail)
                    .font(.system(size: 12.5))
                    .foregroundStyle(WTheme.inkTertiary)
                    .lineLimit(1)
            }
        }
        .frame(minHeight: 30)
        .transition(.opacity.combined(with: .offset(y: 6)))
    }

    @ViewBuilder
    private var status: some View {
        if step.done {
            Image(systemName: "checkmark")
                .font(.system(size: 7.5, weight: .heavy))
                .foregroundStyle(Remie.emerald)
                .frame(width: 14, height: 14)
                .background(Remie.emerald.opacity(0.14), in: Circle())
                .transition(.scale(scale: 0.5).combined(with: .opacity))
        } else if streaming {
            RemieSpinner()
        } else {
            Image(systemName: "xmark")
                .font(.system(size: 7.5, weight: .heavy))
                .foregroundStyle(WTheme.inkTertiary)
                .frame(width: 14, height: 14)
                .background(WTheme.fill, in: Circle())
        }
    }

    private struct LabelStyle: ViewModifier {
        let active: Bool
        func body(content: Content) -> some View {
            if active {
                content.remieShimmer()
            } else {
                content.foregroundStyle(WTheme.ink.opacity(0.8))
            }
        }
    }
}

/// A slate ring with a sky arc turning on it.
struct RemieSpinner: View {
    var size: CGFloat = 14

    var body: some View {
        TimelineView(.animation) { context in
            let angle = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 0.8) / 0.8 * 360
            ZStack {
                Circle().stroke(WTheme.hairlineStrong, lineWidth: 1.5)
                Circle()
                    .trim(from: 0, to: 0.25)
                    .stroke(Remie.sky500, style: StrokeStyle(lineWidth: 1.5, lineCap: .round))
                    .rotationEffect(.degrees(angle))
            }
        }
        .frame(width: size - 1.5, height: size - 1.5)
    }
}

/// The live run's status line: the mark at work, a shimmering phase, the
/// elapsed time and the step count.
struct RemieWorkingStatus: View {
    let label: String
    var since: Date?
    var step = 0
    var budget = 0

    var body: some View {
        HStack(spacing: 10) {
            RemieMark(size: 20, state: .thinking)
            Text(label)
                .font(.system(size: 15, weight: .medium))
                .remieShimmer()
                .id(label)
                .transition(.asymmetric(
                    insertion: .opacity.combined(with: .offset(y: 4)),
                    removal: .opacity.combined(with: .offset(y: -4))
                ))
            if let since {
                TimelineView(.periodic(from: .now, by: 0.1)) { context in
                    Text(Self.elapsed(context.date.timeIntervalSince(since)))
                        .font(WFont.meta(12))
                        .foregroundStyle(WTheme.inkTertiary)
                        .monospacedDigit()
                }
            }
            if step > 0, budget > 0 {
                Text("step \(step) of \(budget)")
                    .font(WFont.meta(11.5))
                    .foregroundStyle(WTheme.inkTertiary.opacity(0.75))
                    .contentTransition(.numericText())
            }
        }
        .animation(.easeOut(duration: 0.18), value: label)
        .accessibilityElement(children: .combine)
    }

    static func elapsed(_ seconds: Double) -> String {
        let s = max(0, seconds)
        if s < 60 { return String(format: "%.1fs", s) }
        return "\(Int(s) / 60)m \(Int(s) % 60)s"
    }
}

/// Under a finished answer: copy it.
struct RemieTurnActions: View {
    let text: String
    @State private var copied = false

    var body: some View {
        HStack(spacing: 2) {
            Button {
                UIPasteboard.general.string = text
                withAnimation(.snappy) { copied = true }
                Task {
                    try? await Task.sleep(for: .seconds(1.6))
                    withAnimation(.snappy) { copied = false }
                }
            } label: {
                Image(systemName: copied ? "checkmark" : "square.on.square")
                    .font(.system(size: 13.5, weight: .medium))
                    .foregroundStyle(copied ? Remie.emerald : WTheme.inkTertiary)
                    .contentTransition(.symbolEffect(.replace))
                    .frame(width: 34, height: 30)
                    .contentShape(Rectangle())
            }
            .buttonStyle(TapScaleStyle())
            .accessibilityLabel(copied ? "Copied" : "Copy answer")
            .sensoryFeedback(.success, trigger: copied) { _, now in now }
        }
        .padding(.leading, -9)
    }
}

// MARK: - Approval

/// A risky tool paused the run. Built for a thumb: what Remie wants to do,
/// the exact arguments, and full-width answers.
struct RemieApprovalCard: View {
    let pending: AgentPendingTool
    let onDecide: (String) -> Void

    private var isSend: Bool { pending.risk == "send" }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                RemieSpark(size: 14, state: .attention)
                Text(isSend ? "Ready to send" : "Needs your approval")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Remie.amber)
                Spacer(minLength: 6)
                Text(isSend ? "Sends email" : "Changes data")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(Remie.amber)
                    .padding(.horizontal, 9)
                    .frame(height: 24)
                    .background(Remie.amber.opacity(0.12), in: Capsule())
            }

            Text(RemieTool.action(pending.toolName))
                .font(.system(size: 19, weight: .semibold))
                .foregroundStyle(WTheme.ink)
                .padding(.top, 12)

            if let summary = pending.argsSummary, !summary.isEmpty {
                Text(summary)
                    .font(WFont.code(13))
                    .lineSpacing(3)
                    .foregroundStyle(WTheme.inkSecondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(12)
                    .background(WTheme.canvas.opacity(0.7), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                    .textSelection(.enabled)
                    .padding(.top, 12)
            }

            Button {
                onDecide("approve")
            } label: {
                HStack(spacing: 7) {
                    Image(systemName: isSend ? "paperplane.fill" : "checkmark")
                        .font(.system(size: 13, weight: .bold))
                    Text(isSend ? "Send" : "Approve")
                        .font(.system(size: 16, weight: .semibold))
                }
                .foregroundStyle(WTheme.inkInverse)
                .frame(maxWidth: .infinity)
                .frame(height: 50)
                .background(WTheme.ink, in: Capsule())
            }
            .buttonStyle(TapScaleStyle())
            .padding(.top, 16)

            HStack(spacing: 10) {
                Button {
                    onDecide("deny")
                } label: {
                    Text("Skip")
                        .font(.system(size: 15, weight: .medium))
                        .foregroundStyle(WTheme.ink)
                        .frame(maxWidth: .infinity)
                        .frame(height: 46)
                        .background(WTheme.fill, in: Capsule())
                }
                .buttonStyle(TapScaleStyle())
                if !isSend {
                    Button {
                        onDecide("always_allow")
                    } label: {
                        Text("Always allow")
                            .font(.system(size: 15, weight: .medium))
                            .foregroundStyle(WTheme.ink)
                            .frame(maxWidth: .infinity)
                            .frame(height: 46)
                            .background(WTheme.fill, in: Capsule())
                    }
                    .buttonStyle(TapScaleStyle())
                    .accessibilityHint("Run this kind of action without asking, for everyone in the workspace")
                }
            }
            .padding(.top, 8)
        }
        .padding(16)
        .background(WTheme.surface, in: RoundedRectangle(cornerRadius: 26, style: .continuous))
        .sensoryFeedback(.warning, trigger: pending.toolCallID)
    }
}

// MARK: - Suggestions

/// `GET /advisor/recommendations`, only the fields Remie suggests from.
struct RemieAdvisorFinding: Decodable, Sendable {
    var id: String
    var detectorKey: String?
    var severity: String?
    var status: String?
    var impact: Double?
    var title: String?
    var groupTitle: String?
    var remedy: String?
    var action: Action?

    struct Action: Decodable, Sendable {
        var label: String?
    }

    enum CodingKeys: String, CodingKey {
        case id, severity, status, impact, title, remedy, action
        case detectorKey = "detector_key"
        case groupTitle = "group_title"
    }

    var rank: Int {
        switch severity {
        case "critical": 4
        case "high": 3
        case "medium": 2
        default: 1
        }
    }
}

/// One thing Remie suggests: the Advisor's grouping of open findings.
struct RemieSuggestion: Identifiable {
    let id: String
    let text: String
    let severity: String
    let findings: [RemieAdvisorFinding]

    var dot: Color {
        switch severity {
        case "critical": Color(hex: 0xF43F5E)
        case "high": Color(hex: 0xF97316)
        case "medium": Color(hex: 0xF59E0B)
        default: Remie.sky500
        }
    }

    /// The same ask the web sends when a suggestion is picked.
    var prompt: String {
        let lines = findings.enumerated().map { i, f in
            "\(i + 1). \(f.title ?? ""). Recommended: \(f.action?.label ?? f.remedy ?? "")"
        }
        return ([findings.count > 1 ? "Please fix these:" : "Please fix this:"] + lines + [
            "Check the current state first, make the smallest change that resolves each one, and ask me before changing anything.",
        ]).joined(separator: "\n")
    }

    static func from(_ findings: [RemieAdvisorFinding]) -> [RemieSuggestion] {
        let open = findings.filter { $0.status == "open" && $0.rank >= 2 }
        var order: [String] = []
        var byKey: [String: [RemieAdvisorFinding]] = [:]
        for f in open {
            let key = f.detectorKey ?? f.id
            if byKey[key] == nil { order.append(key) }
            byKey[key, default: []].append(f)
        }
        var out: [RemieSuggestion] = []
        for key in order {
            guard let members = byKey[key], let lead = members.first else { continue }
            if members.count >= 2, let group = lead.groupTitle, !group.isEmpty {
                out.append(RemieSuggestion(
                    id: key,
                    text: group.replacingOccurrences(of: "{count}", with: "\(members.count)"),
                    severity: lead.severity ?? "low",
                    findings: members
                ))
            } else {
                for f in members {
                    out.append(RemieSuggestion(id: f.id, text: f.title ?? "", severity: f.severity ?? "low", findings: [f]))
                }
            }
        }
        return out.sorted { a, b in
            let ra = a.findings.first?.rank ?? 0, rb = b.findings.first?.rank ?? 0
            if ra != rb { return ra > rb }
            if a.findings.count != b.findings.count { return a.findings.count > b.findings.count }
            return (a.findings.first?.impact ?? 0) > (b.findings.first?.impact ?? 0)
        }
    }
}
