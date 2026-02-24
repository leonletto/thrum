import * as React from "react"
import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-sm text-sm font-medium font-mono uppercase tracking-wide transition-all focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--accent-color)] disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default:
          "bg-gradient-to-br from-[var(--accent-hover)] to-[var(--accent-color)] text-white border border-[var(--accent-color)] shadow-[0_0_15px_var(--accent-glow-soft)] hover:shadow-[0_0_25px_var(--accent-glow-hover)]",
        destructive:
          "bg-red-500 text-white border border-red-600 shadow-[0_0_10px_rgba(239,68,68,0.5)] hover:bg-red-600",
        outline:
          "border border-[var(--accent-border)] bg-transparent text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)] hover:border-[var(--accent-border-hover)]",
        secondary:
          "bg-slate-800/50 text-slate-300 border border-slate-700 hover:bg-slate-800/80",
        ghost: "text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)]",
        link: "text-[var(--accent-color)] underline-offset-4 hover:underline",
      },
      size: {
        default: "h-9 px-4 py-2",
        sm: "h-8 px-3 text-xs",
        lg: "h-10 px-8",
        icon: "h-9 w-9",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button"
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        ref={ref}
        {...props}
      />
    )
  }
)
Button.displayName = "Button"

export { Button, buttonVariants }
