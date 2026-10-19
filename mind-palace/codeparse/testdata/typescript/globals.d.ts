// A declaration file: ambient declarations, declare module and global
// augmentation.

/// <reference types="node" />

export declare function configure(options: Config): void;

export declare class Client {
  constructor(url: string);
  private token;
  request<T>(path: string): Promise<T>;
  close(): void;
}

export declare const VERSION: string;

declare let debug: boolean;

export interface Config {
  url: string;
  retries?: number;
}

declare namespace Telemetry {
  function track(event: string): void;
  const enabled: boolean;
  interface Event {
    name: string;
  }
}

declare module "virtual:config" {
  export const config: Config;
  export default config;
}

declare global {
  interface Window {
    __client: Client;
  }
}

export as namespace ClientLib;
