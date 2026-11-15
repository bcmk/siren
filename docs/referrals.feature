Feature: Referrals
  A referral link earns subscriptions for the user who shares it, the referrer,
  and for the new user who follows it, the referee.
  A new user is one the site's bots have never heard from.
  docs/start-links.md covers the start links that carry referral ids.
  A chat's limit is its max_subs, and a new chat starts at the config's max_subs.

  Rule: A chat's referral id is made once, and works on its bot alone

    Scenario: A chat asks for its referral link
      Given a chat with no referral id
      When it types /more_subs, /referral, /want_more or /buy_subs, which answer the same
      Then the bot makes it an id of five random lowercase letters, unique on that bot
      And answers with the link t.me/<bot>?start=<id>, referral_bonus and referee_bonus

    Scenario: A user on two bots
      Given a user has chats on bots X and Y
      When they type /referral on each
      Then each bot shows a link with the chat's own id

  Rule: A referral link credits both sides when it brings a new user

    Scenario: A new user follows a referral link
      Given the referrer's chat on bot X has the referral id R
      And the site's bots have never heard from the referee
      When the referee sends /start R to X from a private chat
      Then the referee's limit on X becomes the config's max_subs plus referee_bonus
      And the referrer's limit on X grows by referral_bonus
      And referral_events records the referrer and the referee
      And the referee is told they got more subscriptions

    Scenario Outline: A referral link applies nothing
      Given the referrer's chat on bot X has the referral id R
      When <chat> sends /start R
      Then it is <answer>
      And no limit changes

      Examples:
        | chat                                         | answer                             |
        | a known user's private chat on X             | told links only work for new users |
        | a new user's private chat on a bot without R | told the link is invalid           |
        | the referrer's chat on X                     | told only that it is its own link  |
        | a group or channel on X                      | sent the start message alone       |

  Rule: A subscribe link records a streamer referral

    Scenario: A start link names a known streamer
      When a user starts a bot with m-<nickname> and the subscription goes through
      Then referral_events records the user as the referee and the streamer, with no referrer
      And no limit changes

    Scenario: A start link names a streamer the bot does not know yet
      When the checker confirms the streamer
      Then referral_events records the referral at the confirmation
      And records none if the checker denies the streamer

  Rule: A group's referral id moves to the supergroup's chat that has none

    Scenario: A supergroup the bots have never heard from
      When a group becomes a supergroup the bots have never heard from
      Then the supergroup's chats keep the group's referral ids

    Scenario Outline: A supergroup the bots already know
      Given a group's chat on bot X has the referral id R
      And the supergroup's chat on X <state>
      When the group becomes the supergroup
      Then <outcome>
      And the group's referral events move to the supergroup

      Examples:
        | state              | outcome                                 |
        | is missing         | the supergroup gets a chat on X, with R |
        | has no referral id | the supergroup's chat on X takes R      |

  Rule: A group's referral id is dropped where the supergroup's chat has its own

    Scenario: The supergroup's chat has its own referral id
      Given a group's chat on bot X has the referral id R
      And the supergroup's chat on X has its own referral id
      When the group becomes the supergroup
      Then R is dropped, and links carrying it read as invalid
      And the group's referral events move to the supergroup
