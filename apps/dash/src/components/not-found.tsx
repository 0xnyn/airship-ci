import { buttonVariants } from "@airship/ui/components/button";
import Link from "next/link";

import { AuthFrame, AuthHeading } from "./auth-frame";

export function NotFound({
  description,
  title,
}: {
  description: string;
  title: string;
}) {
  return (
    <AuthFrame>
      <AuthHeading eyebrow="Airship" title={title}>
        {description}
      </AuthHeading>
      <div>
        <Link className={buttonVariants({ variant: "outline" })} href="/">
          Go home
        </Link>
      </div>
    </AuthFrame>
  );
}
