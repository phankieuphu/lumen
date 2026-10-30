// Inline stroke icons, drawn in currentColor. Decorative unless labelled.
import type { ReactNode } from "react";

const paths = {
  database: (
    <>
      <ellipse cx="12" cy="6" rx="7" ry="3" />
      <path d="M5 6v12c0 1.66 3.13 3 7 3s7-1.34 7-3V6M5 12c0 1.66 3.13 3 7 3s7-1.34 7-3" />
    </>
  ),
  code: <path d="M8 9l-3 3 3 3M16 9l3 3-3 3M13 7l-2 10" />,
  plug: <path d="M9 3v5M15 3v5M7 8h10v4a5 5 0 01-10 0V8zM12 17v4" />,
  clipboard: <path d="M9 4h6v3H9zM9 5.5H6v15h12v-15h-3M9 12h6M9 16h4" />,
  plus: <path d="M12 5v14M5 12h14" />,
  lock: (
    <>
      <rect x="5" y="11" width="14" height="9" rx="2" />
      <path d="M8 11V8a4 4 0 018 0v3" />
    </>
  ),
  play: <path d="M7 5l12 7-12 7V5z" fill="currentColor" />,
  stop: <rect x="6" y="6" width="12" height="12" rx="1.5" fill="currentColor" />,
  check: <path d="M5 12.5l4.5 4.5L19 7.5" />,
  shield: <path d="M12 3l7 3v5c0 4.5-3 8.2-7 10-4-1.8-7-5.5-7-10V6l7-3z" />,
  warning: <path d="M12 4l9 16H3L12 4zM12 10v4M12 17v.5" />,
  key: (
    <>
      <circle cx="8" cy="15" r="4" />
      <path d="M11 12l8-8M16 7l3 3" />
    </>
  ),
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof paths;

interface Props {
  name: IconName;
  size?: number;
  /** Set for an icon that carries meaning on its own; omit for decoration. */
  label?: string;
}

export function Icon({ name, size = 16, label }: Props) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="round"
      strokeLinejoin="round"
      {...(label ? { role: "img", "aria-label": label } : { "aria-hidden": true })}
    >
      {paths[name]}
    </svg>
  );
}
