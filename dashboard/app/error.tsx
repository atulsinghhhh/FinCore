"use client";

export default function Error({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <div className="mx-auto mt-20 max-w-xl">
      <span className="inline-flex items-center gap-2 rounded-tag border border-pulse px-4 py-1 text-caption uppercase">
        <span className="h-1.5 w-1.5 rounded-full bg-pulse" aria-hidden />
        Error
      </span>
      <h1 className="mt-6 text-heading">Something went wrong.</h1>
      <p className="mt-4 text-body text-warm-gray">{error.message}</p>
      <p className="mt-2 text-data text-warm-gray">
        Is the FinCore API running (<code>make api</code>) and is <code>ADMIN_API_TOKEN</code> set in <code>dashboard/.env.local</code>?
      </p>
      <button onClick={reset} className="mt-8 inline-flex rounded-pill bg-ink px-6 py-4 text-body text-white hover:opacity-80">
        Try again
      </button>
    </div>
  );
}
