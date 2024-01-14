declare global {
    namespace App {
        
        export type InstallStatus = {
            DesktopVersion: string,
            PhpVersion: string | null,
            PhpMyAdminVersion: string | null,
            PMAThemes: string[],
        }
        
    }
}
export default globalThis
