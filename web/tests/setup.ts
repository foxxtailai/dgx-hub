import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';

// jsdom does not implement the native dialog lifecycle. Browser smoke tests
// separately exercise focus trapping, selection and real scrolling.
HTMLDialogElement.prototype.showModal = function () { this.open = true; };
HTMLDialogElement.prototype.close = function () { this.open = false; };
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });
