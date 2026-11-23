import "server-only";

import { cookies } from "next/headers";

// Like Razorpay, test and live are separate worlds: each mode points at its
// own FinCore API (with its own database and keys). The dashboard just
// chooses which one to talk to.

export type Mode = "test" | "live";
export const MODE_COOKIE = "fincore_mode";

type Target = { url: string; token: string };

export function modeTarget(mode: Mode): Target | null {
  if (mode === "live") {
    const url = process.env.FINCORE_LIVE_API_URL;
    const token = process.env.FINCORE_LIVE_ADMIN_TOKEN;
    return url && token ? { url, token } : null;
  }
  const url = process.env.FINCORE_TEST_API_URL ?? process.env.FINCORE_API_URL ?? "http://localhost:8080";
  const token = process.env.FINCORE_TEST_ADMIN_TOKEN ?? process.env.ADMIN_API_TOKEN;
  return token ? { url, token } : null;
}

/**
 * Where operator accounts live. Defaults to the test-mode API; point it at a
 * dedicated deployment (or replace with SSO) in production.
 */
export function authTarget(): Target {
  const url = process.env.FINCORE_AUTH_API_URL;
  const token = process.env.FINCORE_AUTH_ADMIN_TOKEN;
  const t = url && token ? { url, token } : modeTarget("test");
  if (!t) throw new Error("No API configured for operator accounts (FINCORE_AUTH_API_URL or the test API)");
  return t;
}

export function liveConfigured(): boolean {
  return modeTarget("live") !== null;
}

export async function currentMode(): Promise<Mode> {
  const chosen = (await cookies()).get(MODE_COOKIE)?.value;
  return chosen === "live" && liveConfigured() ? "live" : "test";
}
