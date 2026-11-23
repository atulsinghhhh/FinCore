import type { CSSProperties, ElementType } from "react";

type Props = {
  as?: ElementType;
  /** Plain words, revealed one by one. */
  text: string;
  /** Emphasised words: each letter flashes neon, then settles. */
  strong?: string;
  /** Words after the emphasis. */
  after?: string;
  className?: string;
  dark?: boolean;
};

/** A headline that rises word by word, with an optional neon-flash phrase. */
export function Title({ as: Tag = "h2", text, strong, after, className = "", dark }: Props) {
  let i = 0;
  const words = (s?: string) =>
    (s ?? "").split(" ").filter(Boolean).map((w) => (
      <span key={`w${i}`} className="word" style={{ "--i": i++ } as CSSProperties}>
        {w}&nbsp;
      </span>
    ));

  const before = words(text);
  const emphasis = strong
    ? strong.split(" ").map((w) => {
        const wi = i++;
        return (
          <strong key={`s${wi}`} className="inline-block whitespace-nowrap">
            {[...w].map((ch, l) => (
              <span key={l} className="letter" style={{ "--i": wi, "--l": l } as CSSProperties}>
                {ch}
              </span>
            ))}
            &nbsp;
          </strong>
        );
      })
    : null;
  const rest = words(after);

  return (
    <Tag className={`will-animate ${dark ? "dark-title" : ""} ${className}`} aria-label={[text, strong, after].filter(Boolean).join(" ")}>
      <span aria-hidden>
        {before}
        {emphasis}
        {rest}
      </span>
    </Tag>
  );
}
