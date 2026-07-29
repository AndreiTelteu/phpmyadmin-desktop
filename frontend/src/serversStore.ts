import { defineStore } from "solidjs-storex";
import { GetServersJSON, SaveServersJSON } from '../bindings/github.com/andreitelteu/phpmyadmin-desktop/app';
import { produce } from "solid-js/store";
import { createEffect } from "solid-js";

type Server = {
    id: string,
    name: string,
    host: string,
    port: number,
    username: string,
    password: string,
    tunnel: {
        enabled: boolean,
        host: string,
        port: number,
        username: string,
        password: string,
        authMethod: 'password' | 'publicKey',
        privateKey: string,
        passphrase: string,
    },
}
type ServersState = {
    list: Server[],
}

const store = defineStore({
    state: { list: []} as ServersState,
    options: {
        persistent: false,
    },
    actions: (state, set) => ({
        set(...data: any[]) {
            // @ts-expect-error
            set(...data);
            console.log('set', data);
        },
        findById(id: string) {
            return state.list.find(s => s.id === id);
        },
        newServer() {
            set(produce(s => {
                s.list.push({
                    // New connections must have a stable ID before their first
                    // autosave. Existing blank legacy IDs are intentionally not
                    // backfilled: the user can delete and recreate those rows.
                    id: crypto.randomUUID(),
                    name: '',
                    host: '',
                    port: 3306,
                    username: '',
                    password: '',
                    tunnel: {
                        enabled: false,
                        host: '',
                        port: 22,
                        username: '',
                        password: '',
                        authMethod: 'password',
                        privateKey: '',
                        passphrase: '',
                    },
                })
            }))
        },
        removeServer(index: number) {
            set(produce(s => {
                if (index >= 0 && index < s.list.length) {
                    s.list.splice(index, 1);
                }
            }))
        },
        updateServer(index: number, data: Partial<Server>) {
            set(produce(s => {
                if (data.tunnel !== undefined) {
                    data.tunnel = Object.assign(s.list[index].tunnel, data.tunnel);
                }
                Object.assign(s.list[index], data);
            }))
        },
    }),
});
export default store;

GetServersJSON().then(res => {
    try {
        let data = JSON.parse(res);
        store()[1].set(data);
    } catch (e) {
        console.error(e);
    }
})
.finally(() => {
    createEffect(() => {
        const data = store()[0];
        SaveServersJSON(JSON.stringify(data, null, 2)).catch(console.error);
    });
});
