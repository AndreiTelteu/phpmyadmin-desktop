import { createSignal, createEffect, onCleanup } from 'solid-js';

export type ThemeChoice = 'system' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

const STORAGE_KEY = 'pmad.theme';

const mql = typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null;

function readChoice(): ThemeChoice {
    const saved = localStorage.getItem(STORAGE_KEY);
    return saved === 'light' || saved === 'dark' || saved === 'system' ? saved : 'system';
}

const [choice, setChoiceSignal] = createSignal<ThemeChoice>(readChoice());

function resolved(): ResolvedTheme {
    const c = choice();
    if (c !== 'system') return c;
    if (!mql) return 'dark';
    return mql.matches ? 'dark' : 'light';
}

function apply() {
    document.documentElement.dataset.theme = resolved();
    document.documentElement.style.colorScheme = resolved();
}

const onMediaChange = () => apply();
if (mql) mql.addEventListener('change', onMediaChange);

createEffect(() => {
    choice();
    apply();
});

export function useTheme() {
    return {
        choice,
        resolved,
        setTheme(next: ThemeChoice) {
            localStorage.setItem(STORAGE_KEY, next);
            setChoiceSignal(next);
        },
        toggle() {
            const next: ThemeChoice = resolved() === 'dark' ? 'light' : 'dark';
            localStorage.setItem(STORAGE_KEY, next);
            setChoiceSignal(next);
        },
    };
}

export function disposeThemeMedia() {
    if (mql) mql.removeEventListener('change', onMediaChange);
}

apply();
