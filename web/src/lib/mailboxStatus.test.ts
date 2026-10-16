import { describe, expect, it } from "vitest";
import { mailboxColdActivity } from "./mailboxStatus";

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
