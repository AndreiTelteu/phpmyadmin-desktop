/* @refresh reload */
import { render } from 'solid-js/web';

import './index.css';
import App from './App';
import { GetServerID } from '../bindings/github.com/andreitelteu/phpmyadmin-desktop/app';
import PMA from './PMA';

GetServerID().then(res => {
    const root = document.getElementById('root') as HTMLElement;
    const app = res === '' ? () => <App /> : () => <PMA serverId={res} />
    root.innerHTML = '';
    render(app, root);
})
.catch(err => {
    console.log('Error GetServerID',err);
});
