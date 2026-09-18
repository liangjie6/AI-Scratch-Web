declare module '@scratch/scratch-vm' {
  const VirtualMachine: new () => {
    attachStorage(storage: ScratchStorageLike): void;
    start?(): void;
    greenFlag?(): void;
    loadProject(input: Uint8Array): Promise<void>;
    saveProjectSb3(): Promise<Uint8Array | Blob>;
    quit?(): void;
  };
  export default VirtualMachine;
}
