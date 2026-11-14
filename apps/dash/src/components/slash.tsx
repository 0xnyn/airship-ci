/** A breadcrumb separator, drawn as a slanted hairline. */
export function Slash() {
  return (
    <svg
      aria-hidden="true"
      className="size-4 shrink-0 text-faint"
      fill="none"
      viewBox="0 0 16 16"
    >
      <path
        d="M10.5 2.5 5.5 13.5"
        stroke="currentColor"
        strokeLinecap="round"
        strokeWidth="1.25"
      />
    </svg>
  );
}
