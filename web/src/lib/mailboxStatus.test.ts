import { describe, expect, it } from "vitest";
import type AccountStatus from "@/lib/api/models/app/analytics/AccountStatus";
import { cloudStandingUnavailable, mailboxColdActivity, reputationOnlyWarning } from "./mailboxStatus";

describe("cold rotation activity", () => {
    it.each(["resting", "reserve"] as const)("shows %s without a campaign on this instance", (state) => {
        expect(mailboxColdActivity({ in_campaign: false, send_lifecycle: { state } })).toEqual({ resting: true, sending: false });
    });

    it.each(["resting", "reserve"] as const)("never reports %s as campaign sending", (state) => {
        expect(mailboxColdActivity({ in_campaign: true, send_lifecycle: { state } })).toEqual({ resting: true, sending: false });
    });

    it("keeps older status responses without lifecycle compatible", () => {
        expect(mailboxColdActivity({ in_campaign: true })).toEqual({ resting: false, sending: true });
        expect(mailboxColdActivity({ in_campaign: false })).toEqual({ resting: false, sending: false });
        expect(mailboxColdActivity()).toEqual({ resting: false, sending: false });
    });

    it("does not call a mailbox sending just because it is available for cold rotation", () => {
        expect(mailboxColdActivity({ in_campaign: false, send_lifecycle: { state: "active" } })).toEqual({ resting: false, sending: false });
    });
});

describe("Cloud standing availability", () => {
    it("tells a missing Cloud report from a real block", () => {
        expect(cloudStandingUnavailable({ reason: "cloud_evidence_unavailable" })).toBe(true);
        expect(cloudStandingUnavailable({ reason: "complaints" })).toBe(false);
        expect(cloudStandingUnavailable(null)).toBe(false);
        expect(cloudStandingUnavailable(undefined)).toBe(false);
    });
});

type Health = Pick<AccountStatus, "health" | "errors" | "warmup_health" | "warmup_placement">;

const band = (state: NonNullable<AccountStatus["warmup_health"]>["state"], reason = "") =>
    ({ state, score: 0, spam_score: 0, reason, partner_mailboxes_7d: 0, partner_domains_7d: 0, partner_organizations_7d: 0, received_7d: 0, senders_7d: 0 });
const warning: Health = { health: { status: "warning", score: 87 }, errors: [] };

describe("reputationOnlyWarning", () => {
    it("treats watch, throttled and a low inbox rate as a slowdown", () => {
        expect(reputationOnlyWarning({ ...warning, warmup_health: band("watch") })).toBe(true);
        expect(reputationOnlyWarning({ ...warning, warmup_health: band("throttled") })).toBe(true);
        expect(reputationOnlyWarning({ ...warning, warmup_placement: { window_days: 7, min_sample: 20, delivered: 30, inbox: 25, tabs: 0, spam: 5, inbox_rate: 83, band: "fair" } })).toBe(true);
    });

    it("keeps everything else a problem", () => {
        expect(reputationOnlyWarning({ ...warning, warmup_health: band("healthy") })).toBe(false);
        expect(reputationOnlyWarning({ ...warning, warmup_health: band("healthy", "cloud_evidence_unavailable") })).toBe(false);
        expect(reputationOnlyWarning({ ...warning, warmup_health: band("watch"), errors: [{ id: "e", error_code: "AUTH", severity: "WARNING", title: "t", message: "m", created_at: new Date() }] })).toBe(false);
        expect(reputationOnlyWarning({ health: { status: "error", score: 50 }, errors: [], warmup_health: band("quarantined") })).toBe(false);
        expect(reputationOnlyWarning({ health: { status: "healthy", score: 100 }, errors: [], warmup_health: band("watch") })).toBe(false);
    });
});
