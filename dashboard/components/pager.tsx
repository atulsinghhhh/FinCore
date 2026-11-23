import Link from "next/link";

/**
 * Cursor pagination for newest-first lists: "Newest" returns to the first
 * page, "Older" follows the API's next_cursor. Cursors are keyset-based, so
 * rows never repeat or go missing while paging.
 */
export function Pager({ href, params, cursor, nextCursor, shown }: { href: string; params: Record<string, string>; cursor?: string; nextCursor: string | null; shown: number }) {
  const url = (c?: string) => {
    const q = new URLSearchParams(params);
    if (c) q.set("cursor", c);
    else q.delete("cursor");
    return q.size ? `${href}?${q}` : href;
  };
  const pill = "rounded-pill border px-4 py-1.5 text-data transition-colors";
  return (
    <nav aria-label="Pagination" className="mt-8 flex flex-wrap items-center justify-between gap-4">
      <span className="text-caption uppercase text-warm-gray">
        {shown} {shown === 1 ? "row" : "rows"}{cursor ? " · older page" : " · newest first"}
      </span>
      <div className="flex gap-2">
        {cursor ? (
          <Link href={url()} className={`${pill} border-mist hover:border-ink`}>← Newest</Link>
        ) : (
          <span className={`${pill} border-mist text-fog`} aria-disabled>← Newest</span>
        )}
        {nextCursor ? (
          <Link href={url(nextCursor)} className={`${pill} border-ink hover:bg-ink hover:text-white`}>Older →</Link>
        ) : (
          <span className={`${pill} border-mist text-fog`} aria-disabled>Older →</span>
        )}
      </div>
    </nav>
  );
}
