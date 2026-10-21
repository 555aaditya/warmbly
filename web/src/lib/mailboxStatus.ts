import type Inbox from "@/lib/api/models/app/emails/Inbox";
import type AccountStatus from "@/lib/api/models/app/analytics/AccountStatus";
import { diagnosticSendingAllowed, diagnosticWarmupActive } from "./diagnosticParticipation";

export type MailboxDisplayStatus = "healthy" | "warming" | "paused" | "inactive";

export function mailboxColdActivity(status?: Pick<AccountStatus, "send_lifecycle" | "in_campaign">) {
    const lifecycle = status?.send_lifecycle;
    const resting = !!lifecycle && lifecycle.state !== "active";
    return { resting, sending: !!status?.in_campaign && !resting };
}

// The standing Warmbly Cloud stands in for while it has no current report: sends wait, but it is no verdict.
export function cloudStandingUnavailable(h?: { reason?: string } | null) {
    return h?.reason === "cloud_evidence_unavailable";
}

// A warning that only reports warmup reputation (watch, throttled, a low inbox rate): sending slows and lifts on its own while warming continues.
export function reputationOnlyWarning(status?: Pick<AccountStatus, "health" | "errors" | "warmup_health" | "warmup_placement">) {
    if (status?.health?.status !== "warning" || status.errors?.length || cloudStandingUnavailable(status.warmup_health)) return false;
    const band = status.warmup_health?.state;
    const placement = status.warmup_placement?.band;
    return band === "watch" || band === "throttled" || placement === "fair" || placement === "poor";
}

// Account activity is not proof of deliverability or reputation readiness.
export default function mailboxDisplayStatus(box: Inbox): MailboxDisplayStatus {
    if (box.status !== "active") return "inactive";
    if (box.test_mode !== "off" && box.warmup && (box.warmup_paused_at || !diagnosticSendingAllowed(box))) return "paused";
    if (diagnosticWarmupActive(box)) return "warming";
    return "healthy";
}
