import { expect, test } from "../lib/proof.ts";
import { env } from "../lib/env.ts";

test.use({ signedIn: false, timezoneId: "UTC" });

test("retired job history stays visible while only active jobs accept run requests", async ({ page, proof }) => {
  test.skip(process.env.QA_RETIRED_JOBS_FIXTURES !== "1", "Set QA_RETIRED_JOBS_FIXTURES=1 on this worktree's localhost admin origin.");
  for (const origin of [env.webURL, env.apiURL]) {
    expect(["localhost", "127.0.0.1", "[::1]"].includes(new URL(origin).hostname)).toBe(true);
  }

  let runRequests = 0;
  let runRequestedAt: string | undefined;
  const at = "2026-10-10T18:31:00Z";
  const historical = "2026-09-17T15:32:37Z";
  const retired = {
    name: "fleet_quarantine", service: "backend", interval_seconds: 300,
    last_started_at: historical, last_finished_at: historical, last_duration_ms: 24,
    last_status: "ok", last_error: "", run_count: 1850, error_count: 0,
    next_run_at: "2026-09-17T15:37:37Z", retired_at: at, updated_at: historical,
  };
  const active = {
    name: "fixture_active_loop", service: "backend", interval_seconds: 300,
    last_started_at: historical, last_finished_at: historical, last_duration_ms: 24,
    last_status: "error", last_error: "Synthetic failure: not production evidence",
    run_count: 2, error_count: 1, next_run_at: at, updated_at: at,
  };

  await page.route(`${env.apiURL}/**`, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const headers = {
      "Cache-Control": "no-store",
      "Access-Control-Allow-Origin": new URL(env.webURL).origin,
      "Access-Control-Allow-Headers": "Authorization, Content-Type",
      "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
    };
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", headers, body: JSON.stringify(body) });
    if (request.method() === "OPTIONS") return route.fulfill({ status: 204, headers });
    if (path === "/v1/auth/config") return json({ captcha: false });
    if (path === "/v1/auth/me") return json({ id: "44444444-4444-4444-8444-444444444444", email: "fixture-admin@example.test", is_admin: true, admin_permissions: (1 << 0) | (1 << 4) | (1 << 11), session_mfa_verified: true });
    if (path === "/admin/jobs" && request.method() === "GET") return json({ data: [retired, { ...active, run_requested_at: runRequestedAt }] });
    if (path === "/admin/jobs/fixture_active_loop/run" && request.method() === "POST") {
      runRequests++;
      runRequestedAt = at;
      return json({ requested: true });
    }
    if (path === "/admin/instance/health") return json({ checks: [], checked_at: at });
    if (path === "/admin/instance/monitoring") return json({ version: "fixture", checked_at: at, coverage: "unavailable", sources: [] });
    return json({ code: "service_unavailable", message: "Not included in this synthetic UI fixture" }, 503);
  });

  await page.addInitScript(() => localStorage.setItem("warmbly_admin_token", JSON.stringify({ access_token: "fixture-not-a-credential", refresh_token: "fixture-not-a-credential", access_token_expires_at: "2099-01-01T00:00:00Z", refresh_token_expires_at: "2099-01-01T00:00:00Z" })));
  await page.goto("/jobs");
  const retiredRow = page.getByRole("row", { name: /fleet_quarantine/ });
  const activeRow = page.getByRole("row", { name: /fixture_active_loop/ });
  await expect(retiredRow.getByText("retired", { exact: true })).toBeVisible();
  await expect(retiredRow.getByText("Last recorded: ok", { exact: true })).toBeVisible();
  await expect(retiredRow.getByText("Not scheduled", { exact: true })).toHaveAttribute("title", /last schedule/);
  await expect(retiredRow.getByRole("button", { name: "Run now" })).toBeDisabled();
  await expect(activeRow.getByRole("button", { name: "Run now" })).toBeEnabled();
  await expect(page.getByText("1 failing", { exact: true })).toBeVisible();
  await proof.chapter("Retired job history", "Local response fixtures only. This illustrates the UI state, not production data or a real scheduler execution.");
  await proof.shot("retired-job-history", { caption: "Synthetic retired job retains 1,850 historical runs and its last status, has no active schedule or Run now action; the separate active failure still counts." });

  await activeRow.getByRole("button", { name: "Run now" }).click();
  await expect(activeRow.getByText("requested", { exact: true })).toBeVisible();
  await expect(activeRow.getByRole("button", { name: "Run now" })).toBeDisabled();
  expect(runRequests).toBe(1);
  await proof.shot("active-job-run-request", { caption: "Synthetic active loop accepts exactly one run request and displays its pending marker. The retired row stays disabled; actual job execution is not tested here." });
});
