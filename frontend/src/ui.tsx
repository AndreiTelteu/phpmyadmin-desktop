export { css, createGlobalStyles } from 'solid-styled-components'
import { css, type StylesArg } from 'solid-styled-components'
import { Tabs as TabsComp } from '@kobalte/core'
import { For, JSX, type Component } from 'solid-js';

type InputType = {
    [key: string]: any;
}
export type Stylesheet<T = InputType> = {
    [key in keyof T]: StylesArg
}
export type StylesheetReturn<T = InputType> = {
    [key in keyof T]: string
}

export type StylesheetFn<T> = (stylesheet: Stylesheet<T>) => StylesheetReturn<T>

export function stylesheet<T = InputType>(stylesheet: Stylesheet<T>): StylesheetReturn<T> {
    Object.entries(stylesheet).forEach(([key, value]) => {
        Object.assign(stylesheet, { [key]: css(value as StylesArg) })
    })
    return stylesheet as StylesheetReturn<T>
}

export function Tabs(props: {
    tabs: {
        label: string,
        content: JSX.Element | Component,
    }[]
}) {
    const s = stylesheet({
        tabs: {
            width: '100%',
            height: 'calc(100vh - 38px)',
            '&[data-orientation="vertical"]': {
                display: 'flex',
            },
        },
        list: {
            position: 'relative',
            display: 'flex',
            '&[data-orientation="horizontal"]': {
                alignItems: 'center',
                borderBottom: '1px solid hsl(240 5% 84%)',
            },
            '&[data-orientation="vertical"]': {
                flexDirection: 'column',
                alignItems: 'stretch',
                borderRight: '1px solid hsl(240 5% 84%)',
            },
            '[data-kb-theme=dark] &': {
                borderColor: '#3f3f46',
            },
        },
        indicator: {
            position: 'absolute',
            backgroundColor: 'hsl(200 98% 39%)',
            transition: 'all 250ms',
            '&[data-orientation="horizontal"]': {
                bottom: '-1px',
                height: '2px',
            },
            '&[data-orientation="vertical"]': {
                right: '-1px',
                width: '2px',
            },
        },
        trigger: {
            display: 'inline-block',
            padding: '8px 16px',
            outline: 'none',
            '&:hover': {
                backgroundColor: 'hsl(0 0% 98%)',
                color: 'hsl(240 5% 34%)',
            },
            '&:focus-visible': {
                backgroundColor: 'hsl(240 5% 96%)',
            },
            '&[data-disabled], &[data-disabled]:hover': {
                opacity: '0.5',
                backgroundColor: 'transparent',
            },
            '[data-kb-theme=dark] &': {
                color: '#ffffffe6',
                '&:hover, &:focus-visible': {
                    backgroundColor: 'rgba(255, 255, 255, 0.05)',
                },
            },
        },
        content: {
            height: '100%',
            overflow: 'auto',
            padding: '16px',
        },
    })
    return (
        <TabsComp.Root aria-label="Main navigation" class={s.tabs}>
            <TabsComp.List class={s.list}>
                <For each={props.tabs} children={(tab) => (
                    <TabsComp.Trigger class={s.trigger} value={tab.label}>
                        {tab.label}
                    </TabsComp.Trigger>
                )} />
                <TabsComp.Indicator class={s.indicator} />
            </TabsComp.List>
            <For each={props.tabs} children={(tab) => (
                <TabsComp.Content class={s.content} value={tab.label}>
                    {tab.content instanceof Function ? <tab.content /> : tab.content}
                </TabsComp.Content>
            )} />
        </TabsComp.Root>
    )
}
