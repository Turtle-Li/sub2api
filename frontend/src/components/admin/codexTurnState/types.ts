/** 已钉入票据的账号：models 只含当前票据有效（未过期）的模型。 */
export interface PinnedAccount {
  id: number
  name: string
  models: string[]
}
