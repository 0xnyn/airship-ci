import { buttonVariants } from "@airship/ui/components/button";
import Link from "next/link";

import { AuthFrame, AuthHeading, authButton } from "./auth-frame";

export function NotFound({
  description,
  title,
}: {
  description: string;
  title: string;
}) {
  return (
    <AuthFrame>
      <AuthHeading title={title}>{description}</AuthHeading>
      <Link
        className={buttonVariants({
          className: authButton,
          variant: "outline",
        })}
        href="/"
      >
        Go home
      </Link>
    </AuthFrame>
  );
}
