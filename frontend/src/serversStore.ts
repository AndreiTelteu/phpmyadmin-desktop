import { defineStore } from "solidjs-storex";
import * as ConfigStore from '../wailsjs/go/wailsconfigstore/ConfigStore';
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
        set(data) {
            set(data);
        },
        newServer() {
            set(produce(s => {
                s.list.push({
                    id: '',
                    name: '',
                    host: '',
                    port: 22,
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
    }),
});
export default store;

ConfigStore.Get('servers.json', 'null').then(res => {
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
        ConfigStore.Set('servers.json', JSON.stringify(data, null, 2));
    });
});
