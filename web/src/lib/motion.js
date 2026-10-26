// Motion that respects "reduce motion": durations drop to zero there.
const reduced = () => typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches;

/** Transition options, or none when the system asks for less motion. */
export function motion(opts = {}) {
  return reduced() ? { ...opts, duration: 0, delay: 0 } : opts;
}

export const ease = (t) => 1 - Math.pow(1 - t, 3); // cubic out
