export default {
  title: 'Balance Migration',
  description: 'Migrate your account balance from New API into your current account',
  adminTitle: 'New API Quota Migration',
  adminDescription: 'Configure New API site credentials and audit cross-site balance migration records',

  directions: {
    from_new_api: 'New API -> Current Site',
    to_new_api: 'Current Site -> New API',
    comingSoon: 'Coming Soon'
  },

  statuses: {
    pending: 'Pending',
    completed: 'Completed',
    failed: 'Failed',
    compensated: 'Rolled Back',
    needs_manual_review: 'Needs Review'
  },

  reasons: {
    NEW_API_USER_ALREADY_MIGRATED: 'This New API account has already completed its one-time balance migration.',
    NEW_API_MIGRATION_IN_PROGRESS: 'A migration for this New API account is currently in progress. Please refresh later.',
    NEW_API_MIGRATION_NEEDS_REVIEW: 'A previous migration attempt for this New API account requires administrator review.',
    NEW_API_QUOTA_EMPTY: 'No migratable regular balance is available on this New API account.'
  },

  user: {
    currentBalance: 'Current Account Balance',
    sourceSite: 'Source New API Site',
    exchangeRate: 'Exchange Rate',
    quotaPerUnit: 'Quota Conversion',
    oneTimeFullNote: 'One-time full migration: confirms and clears regular balance on New API, once per New API account.',
    bidirectionalRoadmap: 'Outbound migration back to New API is planned for a future release.',
    disabledTitle: 'Balance Migration Unavailable',
    disabledDescription: 'The administrator has not enabled New API balance migration yet.',
    accessTokenLabel: 'New API Personal Access Token',
    accessTokenPlaceholder: 'Enter your New API access token (e.g. nap_...)',
    accessTokenHint: 'Generate an access token in New API (Security Settings -> Access Tokens) with at least the profile:read scope. Your token is used only for one-time verification and is never stored.',
    queryButton: 'Verify & Query Balance',
    querying: 'Querying...',
    previewTitle: 'Migration Preview',
    newApiAccount: 'New API Account',
    newApiUserId: 'New API UID',
    regularBalance: 'Migratable Regular Balance',
    giftBalance: 'Gift Balance (Not Migrated)',
    giftQuotaNotice: 'Your New API account has temporary/gift quota ({amount}). Gift quota is not included in migration and will remain on New API.',
    estimatedCredit: 'Estimated Credit Amount',
    estimatedNewBalance: 'Balance After Migration',
    oneTimeWarning: 'Irreversible one-time action: confirming will zero out this New API account\'s regular balance and credit the converted USD amount to your current account. Each New API account can only be migrated once.',
    confirmButton: 'Confirm Full Migration',
    migrating: 'Migrating...',
    confirmDialogTitle: 'Confirm One-Time Full Migration',
    confirmDialogMessage: 'You are about to migrate all regular balance ({rawUsd}) from New API account {account} (#{uid}) and credit {credit} to your current account. This clears the New API regular balance and cannot be undone.',
    successTitle: 'Migration Completed',
    successMessage: 'Successfully migrated {credit} from New API account {account} (#{uid}). Your new balance is {newBalance}.',
    historyTitle: 'My Migration History',
    historyEmpty: 'No migration records yet',
    columns: {
      time: 'Time',
      direction: 'Direction',
      newApiAccount: 'New API Account',
      newApiQuota: 'New API Quota',
      creditedAmount: 'Credited Amount',
      status: 'Status'
    }
  },

  admin: {
    configTitle: 'New API Site Configuration',
    configDescription: 'Configure the target New API instance and its administrator access token used to read and deduct user regular quotas.',
    enabledLabel: 'Enable New API Balance Migration',
    enabledHint: 'When enabled, logged-in users see the Balance Migration menu and can perform a one-time full migration from New API.',
    migrateFromLabel: 'Allow Migration Out of New API (New API -> Current Site)',
    migrateToLabel: 'Allow Migration Into New API (Current Site -> New API)',
    migrateToHint: 'Reserved for future bidirectional migration support.',
    baseUrlLabel: 'New API Base URL',
    baseUrlPlaceholder: 'https://new-api.example.com',
    baseUrlHint: 'Root URL of the New API site without trailing /api.',
    siteNameLabel: 'Display Site Name (Optional)',
    siteNamePlaceholder: 'Defaults to New API system name',
    adminTokenLabel: 'New API Admin Access Token',
    adminTokenPlaceholderConfigured: 'Configured (leave blank to keep existing token)',
    adminTokenPlaceholderEmpty: 'Enter New API admin/root access token (nap_...)',
    adminTokenHint: 'Stored with AES-256-GCM encryption. Create a scoped token in New API with profile:read, user:read, and user:write scopes. A Root account token is recommended so all user roles can be migrated.',
    autoDetectQuotaLabel: 'Auto-detect quota_per_unit from New API /api/status',
    quotaPerUnitLabel: 'Fallback Quota Per $1 USD',
    exchangeRateLabel: 'Exchange Rate Multiplier',
    exchangeRateHint: 'Converted credit = (regular_quota / quota_per_unit) × exchange_rate. Default 1.0 (1:1 USD).',
    allowInsecureHttpLabel: 'Allow HTTP / Private Network Hosts',
    allowInsecureHttpHint: 'Enable only when connecting to a self-hosted New API instance over HTTP or Docker internal network.',
    testButton: 'Test Connection',
    testing: 'Testing...',
    saveButton: 'Save Configuration',
    saving: 'Saving...',
    saveSuccess: 'New API migration configuration saved',
    testSuccess: 'Connected to {site} (v{version}), quota_per_unit={quotaPerUnit}',
    testAdminRoleRoot: 'Verified Root Token ({username} #{id})',
    testAdminRoleAdmin: 'Verified Admin Token ({username} #{id})',
    testScopeOk: 'user:read scope verified',
    stats: {
      totalCount: 'Total Migrations',
      completedCount: 'Completed',
      totalMigratedBalance: 'Total Credited (USD)',
      needsManualReviewCount: 'Needs Manual Review'
    },
    recordsTitle: 'Migration Audit Records',
    searchPlaceholder: 'Search email, New API username or UID...',
    allStatuses: 'All Statuses',
    allDirections: 'All Directions',
    emptyRecords: 'No migration records found',
    columns: {
      time: 'Time',
      direction: 'Direction',
      sub2apiUser: 'Sub2API User',
      newApiUser: 'New API User',
      quotaChange: 'New API Quota',
      balanceChange: 'Sub2API Balance',
      status: 'Status',
      clientIp: 'Client IP'
    }
  }
}
