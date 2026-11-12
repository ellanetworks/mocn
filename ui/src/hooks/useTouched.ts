import { useState } from "react";

export function useTouched() {
  const [touched, setTouched] = useState<ReadonlySet<string>>(() => new Set());

  return {
    touch: (key: string) => () =>
      setTouched((t) => (t.has(key) ? t : new Set(t).add(key))),
    shown: (key: string, error: string | undefined) =>
      touched.has(key) ? error : undefined,
  };
}
