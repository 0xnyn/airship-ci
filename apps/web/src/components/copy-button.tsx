"use client";

import { Button } from "@airship/ui/components/button";
import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

const RESET_MS = 1500;

export function CopyButton({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), RESET_MS);
    } catch {
      toast.error("Could not copy to the clipboard");
    }
  };

  return (
    <Button aria-label={label} onClick={copy} size="icon-sm" variant="ghost">
      {copied ? <Check /> : <Copy />}
    </Button>
  );
}

/** A block of copyable text, such as a token or a command. */
export function CodeBlock({ label, value }: { label: string; value: string }) {
  return (
    <div className="relative">
      <pre className="overflow-x-auto bg-muted p-3 pr-10 font-mono text-[11px]/relaxed">
        {value}
      </pre>
      <div className="absolute top-1 right-1">
        <CopyButton label={label} value={value} />
      </div>
    </div>
  );
}
