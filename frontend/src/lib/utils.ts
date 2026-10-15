import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

// cn merges conditional class lists and resolves Tailwind conflicts so a
// caller's className reliably overrides a component's defaults.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
