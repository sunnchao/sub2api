export default {
  title: '额度迁移',
  description: '将您在 New API 站点的账户余额迁入当前站点账户',
  adminTitle: 'New API 额度迁移',
  adminDescription: '配置 New API 站点连接凭证并审计全站用户额度迁移流水',

  directions: {
    from_new_api: 'New API -> 本站',
    to_new_api: '本站 -> New API',
    comingSoon: '敬请期待'
  },

  statuses: {
    pending: '处理中',
    completed: '已完成',
    failed: '失败',
    compensated: '已回滚',
    needs_manual_review: '需人工核查'
  },

  reasons: {
    NEW_API_USER_ALREADY_MIGRATED: '该 New API 账号已完成过一次全额迁出，不可重复迁移。',
    NEW_API_MIGRATION_IN_PROGRESS: '该 New API 账号当前有迁移任务正在处理中，请稍后刷新查看。',
    NEW_API_MIGRATION_NEEDS_REVIEW: '该 New API 账号存在待人工核查的迁移记录，请联系管理员处理。',
    NEW_API_QUOTA_EMPTY: '该 New API 账号当前没有可迁移的常规余额。'
  },

  user: {
    currentBalance: '当前账户余额',
    sourceSite: '来源 New API 站点',
    exchangeRate: '换算倍率',
    quotaPerUnit: '额度换算基准',
    oneTimeFullNote: '一次性全额迁出：确认后将清零 New API 账号的常规余额，每个 New API 账号仅支持迁出一次。',
    bidirectionalRoadmap: '双向迁移架构已预留，反向迁出至 New API 将在后续版本开放。',
    disabledTitle: '额度迁移未开放',
    disabledDescription: '管理员尚未开启 New API 额度迁移功能。',
    accessTokenLabel: 'New API 访问令牌',
    accessTokenPlaceholder: '请输入您在 New API 的个人访问令牌（如 nap_...）',
    accessTokenHint: '请在 New API「个人设置 -> 安全设置 -> 访问令牌」中创建包含 profile:read 权限的访问令牌。您的个人令牌仅用于本次归属验证，系统不会保存。',
    queryButton: '验证并查询余额',
    querying: '查询中...',
    previewTitle: '迁移预览确认',
    newApiAccount: 'New API 账号',
    newApiUserId: 'New API UID',
    regularBalance: '可迁移常规余额',
    giftBalance: '赠送/临时余额（不迁移）',
    giftQuotaNotice: '检测到该 New API 账号包含临时/赠送额度（{amount}），赠送额度不参与迁移，将保留在 New API 原站点。',
    estimatedCredit: '预计入账金额',
    estimatedNewBalance: '迁移后预计余额',
    oneTimeWarning: '重要提示：本操作为一次性全额迁出且不可撤销。确认后将扣减清零该 New API 账号的常规余额并入账至当前账户，每个 New API 账号仅可迁出一次。',
    confirmButton: '确认全额迁入',
    migrating: '迁移中...',
    confirmDialogTitle: '确认一次性全额迁入',
    confirmDialogMessage: '即将从 New API 账号 {account} (#{uid}) 全额迁出常规余额（{rawUsd}），换算后向当前账户入账 {credit}。操作将清零原站点常规余额且不可撤销，是否继续？',
    successTitle: '迁移成功',
    successMessage: '已成功从 New API 账号 {account} (#{uid}) 迁入 {credit}，当前最新余额为 {newBalance}。',
    historyTitle: '我的迁移记录',
    historyEmpty: '暂无迁移记录',
    columns: {
      time: '时间',
      direction: '方向',
      newApiAccount: 'New API 账号',
      newApiQuota: 'New API 扣减额度',
      creditedAmount: '本站入账金额',
      status: '状态'
    }
  },

  admin: {
    configTitle: 'New API 站点配置',
    configDescription: '配置目标 New API 站点地址及管理员访问令牌（用于权威校验并扣减用户常规额度）。',
    enabledLabel: '启用 New API 额度迁移',
    enabledHint: '开启后，用户侧边栏将显示「额度迁移」入口，支持用户自助完成一次性全额迁入。',
    migrateFromLabel: '允许从 New API 迁入本站（New API -> 本站）',
    migrateToLabel: '允许从本站迁出至 New API（本站 -> New API）',
    migrateToHint: '已预留双向迁移结构与枚举，后续版本开放。',
    baseUrlLabel: 'New API 站点地址',
    baseUrlPlaceholder: 'https://new-api.example.com',
    baseUrlHint: '填写 New API 站点根地址，无需包含末尾的 /api。',
    siteNameLabel: '站点展示名称（可选）',
    siteNamePlaceholder: '留空则自动使用 New API 站点名称',
    adminTokenLabel: 'New API 管理员访问令牌',
    adminTokenPlaceholderConfigured: '已加密配置（留空则保持原有令牌不变）',
    adminTokenPlaceholderEmpty: '请输入 New API 管理员/Root 访问令牌（nap_...）',
    adminTokenHint: '采用 AES-256-GCM 加密存储。请在 New API 为管理员或 Root 账号创建包含 profile:read、user:read、user:write 权限的访问令牌（推荐使用 Root 账号令牌以支持迁移所有角色用户）。',
    autoDetectQuotaLabel: '自动从 New API /api/status 读取 quota_per_unit',
    quotaPerUnitLabel: '默认每 1 美元对应 Quota',
    exchangeRateLabel: '余额换算倍率',
    exchangeRateHint: '实际入账金额 = (New API 常规余额 / quota_per_unit) × 换算倍率，默认 1.0 表示 1:1 等额换算。',
    allowInsecureHttpLabel: '允许 HTTP / 内网地址连接',
    allowInsecureHttpHint: '仅当 New API 部署在本地/内网 HTTP 或 Docker 内网时开启。',
    testButton: '测试连接',
    testing: '测试中...',
    saveButton: '保存配置',
    saving: '保存中...',
    saveSuccess: 'New API 额度迁移配置已保存',
    testSuccess: '连接成功：{site} (v{version})，quota_per_unit={quotaPerUnit}',
    testAdminRoleRoot: '已验证 Root 令牌（{username} #{id}）',
    testAdminRoleAdmin: '已验证管理员令牌（{username} #{id}）',
    testScopeOk: 'user:read 权限校验通过',
    stats: {
      totalCount: '总迁移笔数',
      completedCount: '成功笔数',
      totalMigratedBalance: '累计迁入总额 (USD)',
      needsManualReviewCount: '待人工核查'
    },
    recordsTitle: '全站迁移审计流水',
    searchPlaceholder: '搜索用户邮箱、New API 用户名或 UID...',
    allStatuses: '全部状态',
    allDirections: '全部方向',
    emptyRecords: '暂无迁移流水记录',
    columns: {
      time: '时间',
      direction: '方向',
      sub2apiUser: '本站用户',
      newApiUser: 'New API 用户',
      quotaChange: 'New API 额度变化',
      balanceChange: '本站余额变化',
      status: '状态',
      clientIp: '来源 IP'
    }
  }
}
