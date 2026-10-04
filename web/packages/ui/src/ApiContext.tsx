import { createContext, useContext } from 'react';
import { api, type ApiClient } from './api';

/** The mounted feature subtree retains its owner for every asynchronous operation. */
export const ApiContext = createContext<ApiClient>(api);
export const useApi = () => useContext(ApiContext);
