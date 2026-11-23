import "server-only";

import { cache } from "react";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { rawApi, type Operator } from "@/lib/api";
import { authTarget } from "@/lib/mode";
import { SESSION_COOKIE, SESSION_TTL_SECONDS, signSession, verifySession } from "@/lib/session-core";

export type Me = Pick<Operator, "id" | "email" | "name" | "role">;

function secret(): string {
  const s = process.env.SESSION_SECRET ?? "";
  if (s.length < 32) throw new Error("SESSION_SECRET must be set to at least 32 characters");
  return s;
}

/**
 * The signed-in operator, re-validated against the API once per request: a
 * disabled account, or a role change (which bumps session_version), ends
 * existing sessions immediately.
 */
export const getSession = cache(async (): Promise<Me | null> => {
  const token = (await cookies()).get(SESSION_COOKIE)?.value;
  const session = token ? await verifySession(token, secret()) : null;
  if (!session) return null;
  try {
    const op = await rawApi<Operator>(authTarget(), `/v1/admin/operators/${session.sub}`);
    if (op.status !== "active" || op.session_version !== session.ver) return null;
    return { id: op.id, email: op.email, name: op.name, role: op.role };
  } catch {
    return null;
  }
});

/** Every data access calls this, so pages are protected even without proxy.ts. */
export async function requireSession(): Promise<Me> {
  const me = await getSession();
  if (!me) redirect("/login");
  return me;
}

/** For mutations: viewers are refused on the server, not just hidden in the UI. */
export async function requireAdmin(): Promise<Me> {
  const me = await requireSession();
  if (me.role !== "admin") throw new Error("Read-only access: only admins can make changes.");
  return me;
}

export async function startSession(op: Operator) {
  const token = await signSession({ sub: op.id, ver: op.session_version, exp: Math.floor(Date.now() / 1000) + SESSION_TTL_SECONDS }, secret());
  (await cookies()).set(SESSION_COOKIE, token, {
    httpOnly: true,
    secure: process.env.NODE_ENV === "production",
    sameSite: "lax",
    path: "/",
    maxAge: SESSION_TTL_SECONDS,
  });
}

export async function endSession() {
  (await cookies()).delete(SESSION_COOKIE);
}

export { safeNextPath as safeNext } from "@/lib/next-path";
