// Is something being dragged over the window? (for the drop hint)
import { writable } from 'svelte/store';
export const dragging = writable(false);
