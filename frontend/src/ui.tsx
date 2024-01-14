export { css, createGlobalStyles } from 'solid-styled-components'
import { css, type StylesArg } from 'solid-styled-components'

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
