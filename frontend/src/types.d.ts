declare global {
    namespace App {
        
        export type InstallStatus = {
            DesktopVersion: string,
            PhpVersion: string | null,
            PhpMyAdminVersion: string | null,
            
        }
        
    }
}
export default globalThis
