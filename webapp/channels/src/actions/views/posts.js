// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import { getChannel } from 'mattermost-redux/selectors/entities/channels';
import { General } from 'mattermost-redux/constants';

// ... existing code ...

export function forwardPost(post, channel, message = '') {
    return async (dispatch, getState) => {
        const state = getState();
        const channelId = channel.id;

        const currentUserId = getCurrentUserId(state);
        const currentTeam = getCurrentTeam(state);

        const relativePermaLink = getPermalinkURL(state, currentTeam.id, post.id);
        const permaLink = `${getSiteURL()}${relativePermaLink}`;

        const license = getLicense(state);
        // ... permission checks ...

        let newPost = {};
        newPost.channel_id = channelId;

        const time = getTimestamp();
        const userId = currentUserId;

        // Determine if source is private
        const sourceChannel = getChannel(state, post.channel_id);
        const isSourcePrivate = sourceChannel && (sourceChannel.type === General.PRIVATE_CHANNEL || sourceChannel.type === General.DM_CHANNEL || sourceChannel.type === General.GM_CHANNEL);

        let quote = '';
        if (post.message) {
            quote = `> ${post.message.replace(/\n/g, '\n> ')}\n\n`;
        }

        // Append permalink ONLY if source is NOT private
        const linkToAppend = isSourcePrivate ? '' : permaLink;
        newPost.message = `${quote}${message ? message + '\n' : ''}${linkToAppend}`;

        // Handle files: fetch from state to ensure createPost has them
        let files = [];
        if (post.file_ids && post.file_ids.length > 0) {
            newPost.file_ids = post.file_ids;
            files = post.file_ids.map((id) => state.entities.files.files[id]).filter(f => f);
        }

        newPost.pending_post_id = `${userId}:${time}`;
        newPost.user_id = userId;
        newPost.create_at = time;
        newPost.metadata = {};
        newPost.props = {};

        // ... hooks and permission checks ...

        const hookResult = await dispatch(runMessageWillBePostedHooks(newPost));

        if (hookResult.error) {
            return hookResult;
        }

        newPost = hookResult.data;

        return dispatch(PostActions.createPost(newPost, files));
    };
}

export function selectAttachmentMenuAction(postId, actionId, cookie, dataSource, text, value) {
    return async (dispatch) => {
        dispatch({
            type: ActionTypes.SELECT_ATTACHMENT_MENU_ACTION,
            data: {
                postId,
                actions: {
                    [actionId]: {
                        text,
                        value,
                    },
                },
            },
        });

        dispatch(PostActions.doPostActionWithCookie(postId, actionId, cookie, value));

        return { data: true };
    };
}
