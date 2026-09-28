use super::OAuthSessionState;

#[test]
fn matching_pending_challenge_claims_its_login_owner() {
    let state = OAuthSessionState::<String>::default();
    let login = state.begin_login();
    let operation = login.token();

    assert_eq!(
        state.publish_challenge(operation, "device-auth\0USER-CODE"),
        Ok(true)
    );
    drop(login);
    assert!(state.snapshot_for_refresh().is_none());

    let claimed = state
        .claim_challenge("device-auth\0USER-CODE")
        .expect("matching challenge should claim its owner");
    assert_eq!(claimed.token(), operation);
    drop(claimed);
}

#[test]
fn stale_pending_challenge_is_rejected_after_logout_or_new_login() {
    let state = OAuthSessionState::<String>::default();
    let login = state.begin_login();
    assert_eq!(
        state.publish_challenge(login.token(), "device-auth\0USER-CODE"),
        Ok(true)
    );
    drop(login);

    let newer_login = state.begin_login();
    assert!(state.claim_challenge("device-auth\0USER-CODE").is_none());
    drop(newer_login);

    let login = state.begin_login();
    assert_eq!(
        state.publish_challenge(login.token(), "device-auth-2\0USER-CODE-2"),
        Ok(true)
    );
    drop(login);
    assert_eq!(state.invalidate(None, || Ok(())), Ok(true));
    assert!(state
        .claim_challenge("device-auth-2\0USER-CODE-2")
        .is_none());
}
