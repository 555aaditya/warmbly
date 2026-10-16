import { describe, expect, it } from "vitest";
import { conditionLabel } from "./meta";

describe("conditionLabel", () => {
    it.each([
        ["confidence", "confidence"],
        ["ai_intent_confidence", "AI intent confidence"],
    ])("preserves the selected operator for %s", (key, label) => {
        for (const [operator, symbol] of [
            ["gte", "≥"],
            ["lte", "≤"],
            ["equals", "is"],
        ]) {
            expect(conditionLabel({ field: "field", key, operator, value: 0.7 })).toBe(`${label} ${symbol} 70%`);
        }
        expect(conditionLabel({ field: "field", key, operator: "exists" })).toBe(`${label} is present`);
    });

    it("keeps legacy confidence conditions as a minimum threshold", () => {
        expect(conditionLabel({ field: "confidence", operator: "", value: 0.7 })).toBe("confidence ≥ 70%");
    });
});
