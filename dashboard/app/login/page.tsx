import { Suspense } from "react";
import { LoginForm } from "./login-form";
import { ThemeToggle } from "@/components/theme-toggle";
import { safeNextPath } from "@/lib/next-path";

export const metadata = { title: "Sign in" };

export default function LoginPage({ searchParams }: PageProps<"/login">) {
  return (
    <div className="grid min-h-screen lg:grid-cols-2">
      <section className="band relative hidden overflow-hidden p-12 lg:flex lg:flex-col">
        <div className="flex items-center gap-2.5">
          <span className="h-3.5 w-3.5 rounded-full bg-pulse" aria-hidden />
          <span className="text-heading-sm">FinCore</span>
        </div>
        <div className="mt-auto">
          <p className="text-[clamp(56px,6vw,96px)] leading-[0.9] tracking-display">
            Every rupee,<br />
            <span className="text-pulse">accounted</span> for.
          </p>
          <p className="mt-8 max-w-md text-body text-fog">The operations console for a payments platform whose ledger always balances.</p>
        </div>
      </section>
      <section className="flex flex-col px-6 py-10 md:px-16">
        <div className="flex justify-end"><ThemeToggle /></div>
        <div className="my-auto w-full max-w-sm self-center">
          <span className="inline-flex items-center rounded-tag border border-pulse px-4 py-1 text-caption uppercase">Operators only</span>
          <h1 className="mt-6 text-heading">Sign in</h1>
          <p className="mt-3 mb-10 text-body text-warm-gray">Use your operator account to continue to the dashboard.</p>
          <Suspense fallback={null}>
            <Form searchParams={searchParams} />
          </Suspense>
        </div>
      </section>
    </div>
  );
}

async function Form({ searchParams }: { searchParams: PageProps<"/login">["searchParams"] }) {
  const sp = await searchParams;
  return <LoginForm next={safeNextPath(sp.next)} />;
}
