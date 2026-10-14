// Product-specific UI switches are injected at build time. Ordinary Memoh
// deployments leave this flag disabled.
export const isSingleBotDeployment = import.meta.env.VITE_SINGLE_BOT === 'true'
