import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

const badgeVariants = cva(
  "inline-flex items-center rounded-sm border px-2.5 py-0.5 text-xs font-semibold font-mono transition-colors focus:outline-none focus:ring-2 focus:ring-[var(--accent-color)] focus:ring-offset-2",
  {
    variants: {
      variant: {
        default:
          "bg-[var(--accent-subtle-bg)] text-[var(--accent-color)] border-[var(--accent-border)] hover:bg-[var(--accent-subtle-bg-hover)]",
        secondary:
          "bg-slate-700/50 text-slate-300 border-slate-600 hover:bg-slate-700/80",
        destructive:
          "bg-red-500 text-white border-red-600 shadow-[0_0_10px_rgba(239,68,68,0.5)] hover:bg-red-600",
        outline: "text-[var(--accent-color)] border-[var(--accent-border)]",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <div className={cn(badgeVariants({ variant }), className)} {...props} />
  )
}

export { Badge, badgeVariants }
