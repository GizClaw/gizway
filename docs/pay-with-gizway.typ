#let accent = rgb("#004d80")
#let ink = rgb("#252a2e")
#let muted = rgb("#66706b")
#let ghost = rgb(0, 0, 0, 50%)
#let key-blue = rgb("#6f91a6")
#let highlight-red = rgb("#c43d3d")
#let highlight(body) = underline(text(fill: highlight-red, style: "italic", weight: "bold", body))
#let key(body) = text(fill: key-blue, size: 8pt, weight: "medium", body)

#let formal-single(
  paper: "a4",
  lang: "zh",
  title: none,
  authors: none,
  department: none,
  font-size: 9.2pt,
  font-family: none,
  margin: 14mm,
  frame-thickness: 3mm,
  frame-outset: 0mm,
  footer: none,
  conference: none,
  dates: none,
  body,
) = {
  set text(font: font-family, size: font-size, lang: lang, hyphenate: true)
  set par(justify: true, spacing: 0.65em)

  show heading.where(level: 2): it => {
    set text(fill: accent, size: font-size, weight: "bold")
    block(above: 3em, below: 0em)[#it]
    v(-0.32em)
    line(length: 100%, stroke: 0.25pt + accent.transparentize(70%))
    v(0.45em)
  }

  let frame = rect(
    width: 100% - frame-thickness,
    height: 100% - frame-thickness,
    stroke: accent + frame-thickness,
    outset: -frame-outset,
  )
  set page(
    width: 210mm,
    height: 500mm,
    margin: margin,
    background: frame,
    footer-descent: -frame-thickness / 2,
    footer: {
      set text(size: font-size, fill: ghost)
      footer
    },
  )

  align(center)[
    #set par(leading: 0.4em)
    #grid(
      rows: 3,
      row-gutter: 0.7em,
      text(size: font-size * 2.5, fill: accent, weight: "medium", title),
      none,
      text(size: font-size * 2.0, authors),
      text(size: font-size * 1.5, style: "italic", department),
    )
  ]
  v(1.25em)
  body
}

#show: formal-single.with(
  paper: "a4",
  lang: "zh",
  title: [Pay with Gizway],
  authors: text(size: 14pt)[利用 AI 调用作为“法币”，实现低费率、低门槛的支付接入],
  department: text(size: 9pt)[Gizway 是 AI 服务的 OpenRouter · GizPay 是 AI 服务的 Stripe],
  font-size: 9.2pt,
  font-family: ("Avenir Next", "PingFang SC"),
  margin: 14mm,
  frame-thickness: 3mm,
  frame-outset: 0mm,
  conference: none,
  dates: none,
  footer: none,
)

#set text(fill: muted)
#set par(leading: 0.66em)

== 谁会使用？

  面向持续使用 AI 的用户（#highlight[几乎所有人]），尤其是：

  #v(0.45em)
  #grid(
    columns: (8.5em, 1fr),
    column-gutter: 0.55em,
    row-gutter: 1em,
    key([想用 AI 赚钱的人]), [通过 AI 产品赚取 AI Token，供自己继续使用 AI 并创造新的产品],
    key([花钱使用 AI 的人]), [充值使用我们的 AI 代理，并可用充值的 AI Token 购买其他支持的服务],
  )

  == 为什么是现在？

  AI 需求正在快速增长，AI 调用正在逐渐成为新时代的“#highlight[法币]”。

  #v(0.45em)
  #grid(
    columns: (5.2em, 1fr),
    column-gutter: 0.55em,
    row-gutter: 1em,
    key([生产资料]), [AI 从偶尔使用的工具，变成持续消耗的生产资料],
    key([供需重合]), [越来越多人同时提供数字服务、消费 AI，收入与生产成本可以在同一体系中衔接],
    key([市场空白]), [目前尚未出现把 AI Gateway 与服务支付闭环结合起来的成熟产品],
  )

  == 解决什么问题？

  #highlight[低费率、低门槛]支付接入，是 GizPay 提供的核心功能。

  #v(0.45em)
  #grid(
    columns: (5.2em, 1fr),
    column-gutter: 0.55em,
    row-gutter: 1em,
    key([AI 聚合]), [一套账户、API Key 与接口，聚合多供应商、多模态及 Realtime 模型（使用 Bifrost 开源方案）],
    key([减少费率]), [收入直接用于 AI 调用，减少银行手续费、跨境汇款费与汇率损失],
    key([支付接入]), [跨境支付落地通常依赖银行账户，涉及繁琐的开户手续、账户费用和持续年检要求；GizPay 减少这些前置条件],
  )

  == 商业模式

  我们用 AI 调用吸引并留住用户，不从调用本身获利；当用户通过 GizPay 支付和结算时，通过#highlight[平台抽成]获得收入。

  #v(0.45em)
  #grid(
    columns: (5.2em, 1fr),
    column-gutter: 0.55em,
    row-gutter: 1em,
    key([用户增长]), [鼓励用户持续使用 AI，扩大用户规模和 Credit 使用需求],
    key([支付收入]), [在 Credit 支付和服务结算过程中收取平台服务费],
  )

  == 市场验证

  OpenRouter 已经验证了统一 AI 服务入口具有#highlight[大规模、持续性]的真实需求。

  #v(0.45em)
  #grid(
    columns: (5.2em, 1fr),
    column-gutter: 0.55em,
    row-gutter: 1em,
    key([平台规模]), [OpenRouter 已处理 100T+ Tokens、数十亿次请求，服务数百万用户],
    key([单模验证]), [DeepSeek V3.1 单一模型四周产生 9,750 万次请求],
    key([数据来源]), [OpenRouter《State of AI 2025》；NIST CAISI],
  )

  == 持久性问题

  AI 调用作为“法币”，通过交易持续积累财富，并为#highlight[自研模型]提供稳定的支持。

  #v(0.45em)
  #grid(
    columns: (5.2em, 1fr),
    column-gutter: 0.55em,
    row-gutter: 1em,
    key([网络效应]), [用户越多，接入的服务越多；服务越多，Credit 的使用场景越丰富],
    key([规模效应]), [调用和支付规模持续增长，进一步降低采购与运营成本],
    key([需求数据]), [真实调用和支付需求帮助我们开发更有价值的专项 API],
    key([自研模型]), [围绕稳定需求开发自研模型，形成长期成本优势和更高利润率],
  )

  == 我们如何开始？

  我们先从 #highlight[VPN] 和经过审核的有限生态起步，跑通 AI 服务支付与 AI 调用消费闭环，最终取得相应的支付牌照。

  #v(0.45em)
  #grid(
    columns: (5.2em, 1fr),
    column-gutter: 0.55em,
    row-gutter: 1em,
    key([起步]), [接入 VPN 和经过审核的有限服务生态],
    key([闭环]), [让服务收入可以直接用于消费 AI 调用],
    key([最终目标]), [取得相应的支付牌照并扩大服务范围],
  )

  == 安全与合规

  我们在#highlight[新加坡]采用符合新加坡法律的方式进行运营。监管依据是新加坡 Payment Services Act 2019，其中对发行人自营服务及有限服务网络使用的 limited-purpose e-money 设有明确规则。

  #v(1.2em)
  #text(fill: ink, weight: "bold")[我们如何符合监管依据？]

  #v(0.55em)
  #grid(
    columns: (5.2em, 1fr),
    column-gutter: 0.55em,
    row-gutter: 1em,
    key([有限用途]), [Credit 仅用于购买 Gizway 自营 AI 服务及审核生态内的数字服务],
    key([有限网络]), [早期仅开放自营服务和少量经过审核、签约接入的服务商],
    key([禁止兑付]), [Credit 不可提现、不回购，不提供公开兑换或场外交易撮合],
    key([退款规则]), [仅允许未使用的自购 Credit 原路退款；转入或经营所得 Credit 不得兑换法币],
    key([风险控制]), [商户审核、分级 KYC / KYB、交易限额、制裁与异常交易筛查、完整账本与冻结能力],
    key([主体与规则]), [以新加坡主体运营，并将 Credit、退款、转移和商户结算边界写入产品规则],
    key([有限上线]), [先接入自营服务和少量审核商户，以私有接口运行并持续审计],
    key([合规扩展]), [开放商户网络或跨境业务前，重新评估监管属性，并申请相应许可或接入持牌支付机构],
  )
