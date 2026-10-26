import React, { useState, useEffect, useRef } from 'react';
import styles from './ChatWindow.module.scss';
import { FaSpinner, FaChevronUp } from 'react-icons/fa';
import type { ChatMessage, GuestChat } from '../../../pages/Chat/ChatPage';
import MessageBubble from '../MessageBubble/MessageBubble';

interface ChatWindowProps {
  messages: ChatMessage[];
  selectedGuest: GuestChat | null;
  onLoadMore: () => void;
  hasMoreMessages: boolean;
  loadingMore: boolean;
  loadingMessages: boolean;
}

const ChatWindow: React.FC<ChatWindowProps> = ({
  messages,
  selectedGuest,
  onLoadMore,
  hasMoreMessages,
  loadingMore,
  loadingMessages
}) => {
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const messagesContainerRef = useRef<HTMLDivElement>(null);
  const [shouldScrollToBottom, setShouldScrollToBottom] = useState(true);

  useEffect(() => {
    if (!messagesContainerRef.current) return;

    const container = messagesContainerRef.current;
    const scrollHandler = () => {
      const isAtBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 50;
      setShouldScrollToBottom(isAtBottom);
    };

    container.addEventListener('scroll', scrollHandler);
    return () => container.removeEventListener('scroll', scrollHandler);
  }, []);

  useEffect(() => {
    if (shouldScrollToBottom) {
      scrollToBottom();
    }
  }, [messages, shouldScrollToBottom]);

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  };

  if (!selectedGuest) {
    return (
      <div className={styles.chatWindow}>
        <div className={styles.noRoomSelected}>
          <p>Select a guest to view their chat</p>
        </div>
      </div>
    );
  }

  return (
    <div className={styles.chatWindow}>
      <div className={styles.chatHeader}>
        <div className={styles.chatRoomInfo}>
          <h2>Room {selectedGuest.room_number} &mdash; {selectedGuest.display_name}</h2>
        </div>
      </div>

      <div className={styles.messagesContainer} ref={messagesContainerRef}>
        {hasMoreMessages && (
          <div className={styles.loadMoreContainer}>
            <button
              className={styles.loadMoreButton}
              onClick={onLoadMore}
              disabled={loadingMore}
            >
              {loadingMore ? (
                <>
                  <FaSpinner className={styles.spinnerIcon} />
                  Loading...
                </>
              ) : (
                <>
                  <FaChevronUp />
                  Load Older Messages
                </>
              )}
            </button>
          </div>
        )}

        {loadingMessages ? (
          <div className={styles.loadingMessages}>
            <FaSpinner className={styles.spinnerIcon} size={24} />
            Loading messages...
          </div>
        ) : messages.length === 0 ? (
          <div className={styles.noMessages}>
            <p>No messages yet</p>
            <small>Start the conversation by sending a message</small>
          </div>
        ) : (
          messages.map((message) => (
            <MessageBubble
              key={message.id}
              message={message}
            />
          ))
        )}
        <div ref={messagesEndRef} />
      </div>

    </div>
  );
};

export default ChatWindow;
