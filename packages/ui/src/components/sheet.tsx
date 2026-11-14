"use client";

import { Button } from "@airship/ui/components/button";
import { cn } from "@airship/ui/lib/utils";
import { Dialog as SheetPrimitive } from "@base-ui/react/dialog";
import { XIcon } from "lucide-react";
import type * as React from "react";
import { useRef } from "react";

/**
 * A panel that slides in from the right edge over the page, for the detail
 * of one row without leaving the list. Built on the dialog primitive, so it
 * traps focus, closes on Escape and returns focus to what opened it.
 */
function Sheet({ ...props }: SheetPrimitive.Root.Props) {
  return <SheetPrimitive.Root data-slot="sheet" {...props} />;
}

function SheetTrigger({ ...props }: SheetPrimitive.Trigger.Props) {
  return <SheetPrimitive.Trigger data-slot="sheet-trigger" {...props} />;
}

function SheetClose({ ...props }: SheetPrimitive.Close.Props) {
  return <SheetPrimitive.Close data-slot="sheet-close" {...props} />;
}

function SheetContent({
  className,
  children,
  initialFocus,
  ...props
}: SheetPrimitive.Popup.Props) {
  const popup = useRef<HTMLDivElement>(null);
  return (
    <SheetPrimitive.Portal>
      <SheetPrimitive.Backdrop
        className="fixed inset-0 z-50 bg-black/20 transition-opacity duration-200 ease-out data-ending-style:opacity-0 data-starting-style:opacity-0 supports-backdrop-filter:backdrop-blur-[1px]"
        data-slot="sheet-overlay"
      />
      <SheetPrimitive.Popup
        className={cn(
          "fixed inset-y-0 right-0 z-50 flex h-dvh w-full flex-col border-l bg-background text-foreground shadow-overlay outline-none transition-transform duration-300 ease-[cubic-bezier(0.32,0.72,0,1)] data-ending-style:translate-x-full data-starting-style:translate-x-full sm:w-[min(26rem,calc(100vw-2rem))] sm:rounded-l-xl md:inset-y-2 md:right-2 md:h-[calc(100dvh-1rem)] md:rounded-xl md:border",
          className
        )}
        data-slot="sheet-content"
        // Opened with a pointer, focus the panel itself so no control lights
        // up with a focus ring; from the keyboard, focus its first control.
        initialFocus={
          initialFocus ??
          ((openType) => (openType === "keyboard" ? true : popup.current))
        }
        ref={popup}
        {...props}
      >
        {children}
      </SheetPrimitive.Popup>
    </SheetPrimitive.Portal>
  );
}

/** The sheet's title bar: what this is, and the way to close it. */
function SheetHeader({
  actions,
  children,
  className,
}: {
  actions?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex shrink-0 items-start gap-2 border-b px-4 py-3",
        className
      )}
      data-slot="sheet-header"
    >
      <div className="grid min-w-0 flex-1 gap-1.5">{children}</div>
      <div className="-mt-0.5 -mr-1.5 flex shrink-0 items-center gap-0.5">
        {actions}
        <SheetPrimitive.Close
          render={<Button size="icon-sm" variant="ghost" />}
        >
          <XIcon />
          <span className="sr-only">Close</span>
        </SheetPrimitive.Close>
      </div>
    </div>
  );
}

function SheetTitle({ className, ...props }: SheetPrimitive.Title.Props) {
  return (
    <SheetPrimitive.Title
      className={cn(
        "break-words font-semibold text-[15px] leading-snug tracking-tight",
        className
      )}
      data-slot="sheet-title"
      {...props}
    />
  );
}

function SheetDescription({
  className,
  ...props
}: SheetPrimitive.Description.Props) {
  return (
    <SheetPrimitive.Description
      className={cn("text-muted-foreground text-sm", className)}
      data-slot="sheet-description"
      {...props}
    />
  );
}

/** The scrolling middle of the sheet. */
function SheetBody({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn("min-h-0 flex-1 overflow-y-auto p-4", className)}
      data-slot="sheet-body"
      {...props}
    />
  );
}

/** Actions pinned to the bottom of the sheet. */
function SheetFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "flex shrink-0 flex-wrap items-center justify-end gap-2 border-t bg-muted/40 px-4 py-2.5",
        className
      )}
      data-slot="sheet-footer"
      {...props}
    />
  );
}

export {
  Sheet,
  SheetBody,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
};
