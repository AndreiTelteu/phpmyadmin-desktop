declare global {
    namespace App {
        
        export type ComponentItem = {
            id: string,
            name: string,
            description: string,
            version?: string,
            latest?: string,
            latest_link?: string,
            img: string,
        }
        
    }
}
export default globalThis
