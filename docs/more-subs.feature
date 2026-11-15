Feature: More subscriptions
  /more_subs shows a chat how to raise its subscription limit:
  share its referral link, or buy subscriptions with Telegram Stars.
  docs/telegram-stars.md explains the payment flow, and docs/referrals.feature covers referrals.
  Buying is on when the config lists subs_tiers, each a count and a total price in Stars.

  Rule: The bot points to /more_subs wherever a chat may want more subscriptions

    Scenario: The command menu and /help
      When a chat opens the command menu or types /help
      Then /more_subs is listed right after /list
      And its aliases are not listed

    Scenario Outline: An answer shows the usage and points to /more_subs
      When <event>
      Then the answer shows the chat's usage and points to /more_subs

      Examples:
        | event                                       |
        | a chat types /settings                      |
        | a chat at its limit tries to add a streamer |

    Scenario: A chat nears its limit
      When a chat adds a streamer the bot already knows
      And is left with heavy_user_remainder or fewer free subscriptions
      Then the answer shows the chat's usage and points to /more_subs

    Scenario: /faq points to /more_subs, and to buying only when buying is on
      When a chat types /faq
      Then the answer points to /more_subs for the referral link
      And mentions buying with Telegram Stars only if buying is on

    Scenario: The ad for buying with Stars runs only when buying is on
      Given the config lists no subs_tiers
      Then the bot never sends the ad for buying with Stars

  Rule: /more_subs answers under its aliases too

    Scenario Outline: A chat asks for more subscriptions
      When a chat types <command>
      Then the bot answers with the chat's usage, its referral link and both referral bonuses
      And how many users registered via its user's referral links, on all the site's bots together

      Examples:
        | command    |
        | /more_subs |
        | /want_more |
        | /referral  |
        | /buy_subs  |

  Rule: The answer offers buying only when buying is on

    Scenario: Buying is on
      Given the config lists subs_tiers
      When a chat types /more_subs
      Then the answer pitches buying with Telegram Stars
      And has a button per tier, labelled with its count and price, and its discount if any
      And the discount compares its price per subscription with the first tier's

    Scenario: Buying is off
      Given the config lists no subs_tiers
      When a chat types /more_subs
      Then the answer neither pitches buying nor has buttons

  Rule: A package tap sends an invoice into the chat

    Scenario: A user taps a package in a private chat
      When the user taps a package
      Then the bot sends an invoice in Stars for the tier's count and price

    Scenario Outline: Someone taps a package in a group or channel
      When <tapper> taps a package
      Then <outcome>

      Examples:
        | tapper                           | outcome                                              |
        | an admin                         | the bot sends the invoice into the chat              |
        | a non-admin, per Telegram        | only they see that admins alone can start a purchase |
        | anyone, if asking Telegram fails | the tap is dropped silently                          |
        | anyone during a shutdown         | the tap is dropped silently                          |

    Scenario: A tap on a package the config no longer lists
      When someone allowed to buy taps a package whose tier the config no longer lists
      Then the bot answers with /more_subs again

    Scenario: A tap with buying off
      Given the config lists no subs_tiers
      When someone taps a package on an older answer
      Then the tap is dropped silently

  Rule: Pre-checkout refuses a payment that no longer matches the config

    Scenario Outline: Pre-checkout refuses a payment
      When a payment reaches pre-checkout <case>
      Then the bot refuses it, saying the invoice is no longer valid

      Examples:
        | case                                      |
        | for a tier the config no longer lists     |
        | for an amount other than the tier's price |
        | while buying is off                       |
        | for a chat outside whitelist_chats        |

  Rule: A payment credits the chat the invoice is for

    Scenario Outline: A payment's receipt arrives
      Given chat C's invoice for N subscriptions
      When someone pays it and the receipt arrives <where>
      Then C's limit on the bot grows by N
      And C is told how many subscriptions were added and its new limit

      Examples:
        | where                       |
        | in C                        |
        | in the payer's private chat |
        | as a post in channel C      |

    Scenario: Telegram redelivers a credited charge
      When the same charge arrives again
      Then no limit changes
      And C is told the payment has already been credited

    Scenario: The tiers change between pre-checkout and the receipt
      When the paid amount no longer matches a listed tier
      Then the bot credits the paid count anyway
      And logs the mismatch for reconciliation

    Scenario: A receipt arrives while the bot cannot store it
      When a receipt arrives during the startup migrations or a shutdown
      Then the webhook answers 503
      And Telegram redelivers the receipt later
