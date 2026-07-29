import { createEffect, createSignal, on, onCleanup, Show } from 'solid-js';

export function Toggle(props: {
    checked: boolean;
    onChange: (checked: boolean) => void;
    label: string;
    description?: string;
    disabled?: boolean;
}) {
    let btnRef: HTMLButtonElement | undefined;
    const [checked, setChecked] = createSignal(props.checked);

    createEffect(on(() => props.checked, (v) => setChecked(v)));

    function toggle() {
        if (props.disabled) return;
        const next = !checked();
        setChecked(next);
        props.onChange(next);
    }

    function onKeyDown(e: KeyboardEvent) {
        if (props.disabled) return;
        if (e.key === 'ArrowLeft' && checked()) {
            e.preventDefault();
            toggle();
        } else if (e.key === 'ArrowRight' && !checked()) {
            e.preventDefault();
            toggle();
        }
    }

    const descId = `switch-desc-${Math.random().toString(36).slice(2, 8)}`;

    return (
        <div class="switch" data-checked={checked()}>
            <button
                ref={btnRef}
                type="button"
                role="switch"
                aria-checked={checked()}
                aria-label={props.label}
                aria-describedby={props.description ? descId : undefined}
                class="switch__track"
                disabled={props.disabled}
                onClick={toggle}
                onKeyDown={onKeyDown}
            />
            <div>
                <div class="switch__label">{props.label}</div>
                {props.description ? (
                    <div class="switch__desc" id={descId}>{props.description}</div>
                ) : null}
            </div>
        </div>
    );
}

export function SettingsFlyout(props: {
    open: boolean;
    onClose: () => void;
    theme: { choice: () => 'system' | 'light' | 'dark'; resolved: () => 'light' | 'dark'; setTheme: (t: 'system' | 'light' | 'dark') => void };
    appVersion: string;
}) {
    let panelRef: HTMLElement | undefined;
    let closeRef: HTMLButtonElement | undefined;
    let previouslyFocused: Element | null = null;

    createEffect(on(() => props.open, (open) => {
        if (open) {
            previouslyFocused = document.activeElement;
            setTimeout(() => closeRef?.focus(), 0);
            document.addEventListener('keydown', onKeyDown, true);
        } else {
            document.removeEventListener('keydown', onKeyDown, true);
            if (previouslyFocused instanceof HTMLElement) previouslyFocused.focus();
        }
    }));

    onCleanup(() => document.removeEventListener('keydown', onKeyDown, true));

    function onKeyDown(e: KeyboardEvent) {
        if (e.key === 'Escape') {
            e.stopPropagation();
            props.onClose();
            return;
        }
        if (e.key !== 'Tab' || !panelRef) return;
        const focusables = panelRef.querySelectorAll<HTMLElement>(
            'button:not(:disabled), [href], input:not(:disabled), [tabindex]:not([tabindex="-1"])'
        );
        if (focusables.length === 0) return;
        const first = focusables[0];
        const last = focusables[focusables.length - 1];
        if (e.shiftKey && document.activeElement === first) {
            e.preventDefault();
            last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
            e.preventDefault();
            first.focus();
        }
    }

    return (
        <Show when={props.open}>
            <div class="overlay" onMouseDown={(e) => { if (e.target === e.currentTarget) props.onClose(); }}>
            <aside
                ref={panelRef}
                class="flyout"
                role="dialog"
                aria-modal="true"
                aria-label="Settings"
            >
                <div class="flyout__head">
                    <h2>Settings</h2>
                    <button ref={closeRef} type="button" class="iconbtn" aria-label="Close settings" onClick={props.onClose}>
                        <CloseIconGlyph />
                    </button>
                </div>
                <div class="flyout__body">
                    <section class="settings-group" aria-label="Appearance">
                        <div class="settings-group__title">Appearance</div>
                        <Toggle
                            checked={props.theme.resolved() === 'dark'}
                            onChange={(on) => props.theme.setTheme(on ? 'dark' : 'light')}
                            label="Dark mode"
                            description={props.theme.choice() === 'system'
                                ? `Currently following the OS (${props.theme.resolved()}). Toggle to override.`
                                : `Saved preference: ${props.theme.choice()}.`}
                        />
                    </section>

                    <section class="settings-group" aria-label="Runtime">
                        <div class="settings-group__title">Runtime</div>
                        <div class="settings-planned">
                            <span class="settings-planned__name">FrankenPHP runtime location</span>
                            <span class="settings-planned__desc">Choose where the app stores the PHP server runtime and generated configuration.</span>
                            <span class="planned-badge">Planned</span>
                        </div>
                        <div class="settings-planned">
                            <span class="settings-planned__name">Automatic component updates</span>
                            <span class="settings-planned__desc">Periodically check for new phpMyAdmin and PHP runtime releases.</span>
                            <span class="planned-badge">Planned</span>
                        </div>
                    </section>

                    <section class="settings-group" aria-label="Connections">
                        <div class="settings-group__title">Connections</div>
                        <div class="settings-planned">
                            <span class="settings-planned__name">Secure credential storage</span>
                            <span class="settings-planned__desc">Move saved passwords into the OS keychain instead of the plain config file.</span>
                            <span class="planned-badge">Planned</span>
                        </div>
                    </section>

                    <section class="settings-group" aria-label="About">
                        <div class="settings-group__title">About</div>
                        <span class="about-version">phpMyAdmin Desktop · v{props.appVersion}</span>
                    </section>
                </div>
            </aside>
            </div>
        </Show>
    );
}

function CloseIconGlyph() {
    return (
        <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
            <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
        </svg>
    );
}
