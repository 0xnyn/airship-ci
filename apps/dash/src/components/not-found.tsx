import { buttonVariants } from "@airship/ui/components/button";
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@airship/ui/components/empty";
import Link from "next/link";

export function NotFound({
  description,
  title,
}: {
  description: string;
  title: string;
}) {
  return (
    <div className="grid min-h-svh place-items-center p-4">
      <Empty>
        <EmptyHeader>
          <EmptyTitle>{title}</EmptyTitle>
          <EmptyDescription>{description}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Link className={buttonVariants({ variant: "outline" })} href="/">
            Go home
          </Link>
        </EmptyContent>
      </Empty>
    </div>
  );
}
